package kuberuntime

import (
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/kubelet-tming/images"
	internalapi "k8s.io/cri-api/pkg/apis"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	kubetypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	utilversion "k8s.io/apimachinery/pkg/util/version"
	"errors"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net"
	"k8s.io/kubernetes/pkg/kubelet/util/logreduction"
	"time"
	"k8s.io/client-go/util/flowcontrol"
	"encoding/json"
	"k8s.io/kubernetes/pkg/api/legacyscheme"

	ref "k8s.io/client-go/tools/reference"
	"k8s.io/kubernetes/pkg/kubelet/events"
	"fmt"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/credentialprovider"
)


const (
	kubeRuntimeAPIVersion	= "0.1.0"

	// The root directory for pod logs
	podLogsRootDirectory = "/var/log/pods"

	// How frequently to report identical errors
	identicalErrorDelay = 1 * time.Minute

	// A minimal shutdown window for avoiding unnecessary SIGKILLs
	minimumGracePeriodInSeconds = 2
)


var (
	// ErrVersionNotSupported is returned when the api version of runtime interface is not supported
	ErrVersionNotSupported = errors.New("Runtime api version is not supported")
)

type podStateProvider interface {
	IsPodDeleted(kubetypes.UID) bool
	IsPodTerminated(kubetypes.UID) bool
}

type kubeGenericRuntimeManager struct {
	runtimeName 		string
	recorder 		record.EventRecorder
	// wrapped image puller.
	imagePuller 		images.ImageManager

	// gRPC service clients
	runtimeService 		internalapi.RuntimeService
	imageService   		internalapi.ImageManagerService

	containerGC 		*containerGC

	// Cache last per-container error message to reduce log spam
	logReduction *logreduction.LogReduction

	// A shim to legacy functions for backward compatibility.
	legacyLogProvider LegacyLogProvider

	// The directory path for seccomp profiles.
	seccompProfileRoot string

	osInterface         kubecontainer.OSInterface

	// Keyring for pulling images
	keyring credentialprovider.DockerKeyring

	// RuntimeHelper that wraps kubelet to generate runtime container options.
	runtimeHelper kubecontainer.RuntimeHelper



}

type containerToKillInfo struct {

	container 	*v1.Container

	name 		string

	message 	string
}

// podActions keeps information what to do for a pod
type podActions struct {
	// Stop all running (regular and init) containers and the sandbox for the pod.
	KillPod 			bool
	// Whether need to create a new sandbox, If needed to kill pod and create
	// a new pod sandbox, all init containers need to purged
	CreateSandbox 			bool

	SandboxID 			string

	Attempt 			uint32

	NextInitContainerToStart 	*v1.Container

	ContainersToStart		[]int

	ContainersToKill		map[kubecontainer.ContainerID]containerToKillInfo
}

func shouldRestartOnFailure(pod *v1.Pod) bool {
	return pod.Spec.RestartPolicy != v1.RestartPolicyNever
}

func containerSucceeded(c *v1.Container, podStatus *kubecontainer.PodStatus) bool {
	cStatus := podStatus.FindContainerStatusByName(c.Name)
	if cStatus == nil || cStatus.State == kubecontainer.ContainerStateRunning {
		return false
	}
	return cStatus.ExitCode == 0
}

func (m *kubeGenericRuntimeManager) podSandboxChanged(pod *v1.Pod, podStatus *kubecontainer.PodStatus) (bool, uint32, string) {
	if len(podStatus.SandboxStatuses) == 0 {
		klog.Infof("No sandbox for pod %q can be found. Need to start a new one", format.Pod(pod))
		return true, 0, ""
	}
	readySandboxCount := 0
	for _, s := range podStatus.SandboxStatuses {
		if s.State == runtimeapi.PodSandboxState_SANDBOX_READY {
			readySandboxCount++
		}
	}

	// Needs to create a new sandbox when readySandboxCount > 1 or the ready sandbox is not the latest one.
	sandboxStatus := podStatus.SandboxStatuses[0]
	if readySandboxCount > 1 {
		klog.V(2).Infof("More than 1 sandboxes for pod %q are ready. Need to reconcile them", format.Pod(pod))
		return true, sandboxStatus.Metadata.Attempt + 1, sandboxStatus.Id
	}
	if sandboxStatus.State != runtimeapi.PodSandboxState_SANDBOX_READY {
		klog.V(2).Infof("No ready sandbox for pod %q can be found. Need to start a new one", format.Pod(pod))
		return true, sandboxStatus.Metadata.Attempt + 1, sandboxStatus.Id
	}

	// Needs to create a new sandbox when network namespace changed.
	//if sandboxStatus.GetLinux().GetNamespaces().GetOptions().GetNetwork() != networkNamespaceForPod(pod) {
	//	klog.V(2).Infof("Sandbox for pod %q has changed. Need to start a new one", format.Pod(pod))
	//	return true, sandboxStatus.Metadata.Attempt + 1, ""
	//}

	// Needs to create a new sandbox when the sandbox does not have an IP address.
	if !kubecontainer.IsHostNetworkPod(pod) && sandboxStatus.Network.Ip == "" {
		klog.V(2).Infof("Sandbox for pod %q has no IP address.  Need to start a new one", format.Pod(pod))
		return true, sandboxStatus.Metadata.Attempt + 1, sandboxStatus.Id
	}

	return false, sandboxStatus.Metadata.Attempt, sandboxStatus.Id

}

func (m *kubeGenericRuntimeManager) computePodActions(pod *v1.Pod, podStatus *kubecontainer.PodStatus) podActions {

	klog.Infof("computePodActions starts!")

	createPodSandbox, attempt, sandboxID := m.podSandboxChanged(pod, podStatus)

	changes := podActions{
		KillPod:		createPodSandbox,
		CreateSandbox: 		createPodSandbox,
		SandboxID: 		sandboxID,
		Attempt: 		attempt,
		ContainersToStart: 	[]int{},
		ContainersToKill: 	make(map[kubecontainer.ContainerID]containerToKillInfo),
	}

	if createPodSandbox {
		if !shouldRestartOnFailure(pod) && attempt != 0 && len(podStatus.ContainerStatuses) != 0 {
			changes.CreateSandbox = false
			return changes
		}

		if len(pod.Spec.InitContainers) != 0 {
			changes.NextInitContainerToStart = &pod.Spec.InitContainers[0]
			return changes
		}

		for idx, c := range pod.Spec.Containers {
			if containerSucceeded(&c, podStatus) && pod.Spec.RestartPolicy == v1.RestartPolicyOnFailure {
				continue
			}
			changes.ContainersToStart = append(changes.ContainersToStart, idx)
		}
		return changes
	}

	// TODO init containers
	// Check initialization progress.
	//initLastStatus, next, done := findNextInitContainerToRun(pod, podStatus)
	//if !done {
	//	if next != nil {
	//		initFailed := initLastStatus != nil && isInitContainerFailed(initLastStatus)
	//		if initFailed && !shouldRestartOnFailure(pod) {
	//			changes.KillPod = true
	//		} else {
	//			// Always try to stop containers in unknown state first.
	//			if initLastStatus != nil && initLastStatus.State == kubecontainer.ContainerStateUnknown {
	//				changes.ContainersToKill[initLastStatus.ID] = containerToKillInfo{
	//					name:      next.Name,
	//					container: next,
	//					message: fmt.Sprintf("Init container is in %q state, try killing it before restart",
	//						initLastStatus.State),
	//				}
	//			}
	//			changes.NextInitContainerToStart = next
	//		}
	//	}
	//	// Initialization failed or still in progress. Skip inspecting non-init
	//	// containers.
	//	return changes
	//}

	keepCount := 0
	for idx, container := range pod.Spec.Containers {
		containerStatus := podStatus.FindContainerStatusByName(container.Name)

		if containerStatus != nil && containerStatus.State != kubecontainer.ContainerStateRunning {
			// TODO PostStopContainer

			//if err := m.internalLifecycle.PostStopContainer(containerStatus.ID.ID); err != nil {
			//	klog.Errorf("internal container post-stop lifecycle hook failed for container %v in pod %v with error %v",
			//		container.Name, pod.Name, err)
			//}
		}

		if containerStatus == nil || containerStatus.State != kubecontainer.ContainerStateRunning {
			if kubecontainer.ShouldContainerBeRestarted(&container, pod, podStatus) {
				message := fmt.Sprintf("Container %+v is dead, but RestartPolicy says that we should restart it.", container)
				klog.Infof(message)

				changes.ContainersToStart = append(changes.ContainersToStart, idx)

				if containerStatus != nil && containerStatus.State == kubecontainer.ContainerStateUnknown {
					changes.ContainersToKill[containerStatus.ID] = containerToKillInfo{
						name: 		containerStatus.Name,
						container: 	&pod.Spec.Containers[idx],
						message: 	fmt.Sprintf("Container is in %q state, try killing it before restart",
							containerStatus.State),
					}
				}
			}
			continue
		}

		var message string
		restart := shouldRestartOnFailure(pod)
		if _, _, changed := containerChanged(&container, containerStatus); changed {
			message = fmt.Sprintf("Container %s definition changed", container.Name)
			restart = true
		} else {
			keepCount++
			continue
		}
		//else if liveness, found := m.livenessManager.Get(containerStatus.ID); found && liveness == proberesults.Failure {
		//	// If the container failed the liveness probe, we should kill it.
		//	message = fmt.Sprintf("Container %s failed liveness probe", container.Name)
		//}

		if restart {
			message = fmt.Sprintf("%s, will be restarted", message)
			changes.ContainersToStart = append(changes.ContainersToStart, idx)
		}

		changes.ContainersToKill[containerStatus.ID] = containerToKillInfo{
			name:				containerStatus.Name,
			container:			&pod.Spec.Containers[idx],
			message:			message,
		}

		klog.Infof("Container %q (%q) of pod %s: %s", container.Name, containerStatus.ID, format.Pod(pod), message)

	}

	// TODO why

	//if keepCount == 0 && len(changes.ContainersToStart) == 0 {
	//	changes.KillPod = true
	//}

	klog.Infof("should not happen!")
	return changes
}

func containerChanged(container *v1.Container, containerStatus *kubecontainer.ContainerStatus) (uint64, uint64, bool) {
	expectedHash := kubecontainer.HashContainer(container)
	return expectedHash, containerStatus.Hash, containerStatus.Hash != expectedHash
}


func (m *kubeGenericRuntimeManager) killPodWithSyncResult(pod *v1.Pod, runningPod kubecontainer.Pod, gracePeriodOverride *int64) (result kubecontainer.PodSyncResult) {
	killContainerResults := m.killContainersWithSyncResult(pod, runningPod, gracePeriodOverride)
	for _, containerResult := range killContainerResults {
		result.AddSyncResult(containerResult)
	}

	killSandboxResult := kubecontainer.NewSyncResult(kubecontainer.KillPodSandbox, runningPod.ID)
	result.AddSyncResult(killSandboxResult)

	for _, podSandbox := range runningPod.Sandboxes {
		if err := m.runtimeService.StopPodSandbox(podSandbox.ID.ID); err != nil {
			killSandboxResult.Fail(kubecontainer.ErrKillPodSandbox, err.Error())
			klog.Errorf("Failed to stop sandbox %q", podSandbox.ID)
		}
	}

	return
}


// SyncPod syncs the running pod into the desired pod by executing following steps:
//
//  1. Compute sandbox and container changes.
//  2. Kill pod sandbox if necessary.
//  3. Kill any containers that should not be running.
//  4. Create sandbox if necessary.
//  5. Create init containers.
//  6. Create normal containers.

func (m *kubeGenericRuntimeManager) SyncPod(pod *v1.Pod, podStatus *kubecontainer.PodStatus, pullSecrets []v1.Secret, backOff *flowcontrol.Backoff) (result kubecontainer.PodSyncResult) {

	p1, _ := json.MarshalIndent(pod, "", "\t")
	p2, _ := json.MarshalIndent(podStatus, "", "\t")
	klog.Infof("kubeGenericRuntimeManager SyncPod pod: %v, podStatus: %v", string(p1), string(p2))

	podContainerChanges := m.computePodActions(pod, podStatus)

	// Step 1: Compute sandbox and container changes.

	if podContainerChanges.CreateSandbox {
		ref, err := ref.GetReference(legacyscheme.Scheme, pod)
		if err != nil {
			klog.Errorf("Couldn't make a ref to pod %q: '%v'", format.Pod(pod), err)
		}
		if podContainerChanges.SandboxID != "" {
			m.recorder.Eventf(ref, v1.EventTypeNormal, events.SandboxChanged, "Pod sandbox changed, it will be killed and re-created.")
		} else {
			klog.Infof("SyncPod received new pod %q, will create a sandbox for it", format.Pod(pod))
		}
	}

	// Step 2: Kill the pod if the sandbox has changed.

	if podContainerChanges.KillPod {
		if podContainerChanges.CreateSandbox {
			klog.Infof("Stopping PodSandbox for %q, will start new one", format.Pod(pod))
		} else {
			klog.Infof("Stopping PodSandbox for %q because all other containers are dead.", format.Pod(pod))
		}

		killResult := m.killPodWithSyncResult(pod, kubecontainer.ConvertPodStatusToRunningPod(m.runtimeName, podStatus), nil)
		result.AddPodSyncResult(killResult)

		if killResult.Error() != nil {
			klog.Errorf("killPodWithSyncResult failed: %v", killResult.Error())
			return
		}

		if podContainerChanges.CreateSandbox {
			m.purgeInitContainers(pod, podStatus)
		}
	} else {

		// Step 3: kill any running containers in this pod which are not to keep.
		for containerID, containerInfo := range podContainerChanges.ContainersToKill {
			klog.Infof("Killing unwanted container %q(id=%q) for pod %q", containerInfo.name, containerID, format.Pod(pod))
			killContainerResult := kubecontainer.NewSyncResult(kubecontainer.KillContainer, containerInfo.name)
			result.AddSyncResult(killContainerResult)
			if err := m.killContainer(pod, containerID, containerInfo.name, containerInfo.message, nil); err != nil {
				killContainerResult.Fail(kubecontainer.ErrKillContainer, err.Error())
				klog.Errorf("killContainer %q(id=%q) for pod %q failed: %v", containerInfo.name, containerID, format.Pod(pod), err)
				return
			}
		}
	}


	// Keep terminated init containers fairly aggressively controlled
	// This is an optimization because container removals are typically handled
	// by container garbage collector.

	// TODO
	//m.pruneInitContainersBeforeStart(pod, podStatus)

	podIP := ""
	if podStatus != nil {
		podIP = podStatus.IP
	}

	klog.Infof("kubeGenericRuntimeManager SyncPod podIP: %v", podIP)

	// Step 4: Create a sandbox for the pod if necessary.
	podSandboxID := podContainerChanges.SandboxID

	if podContainerChanges.CreateSandbox {
		var msg string
		var err error

		klog.Infof("Creating sandbox for pod: %q", format.Pod(pod))
		createSandboxResult := kubecontainer.NewSyncResult(kubecontainer.CreatePodSandbox, format.Pod(pod))
		result.AddSyncResult(createSandboxResult)

		podSandboxID, msg, err = m.createPodSandbox(pod, podContainerChanges.Attempt)

		if err != nil {
			createSandboxResult.Fail(kubecontainer.ErrCreatePodSandbox, msg)
			klog.Errorf("createPodSandbox for pod %q failed: %v", format.Pod(pod), err)
			ref, referr := ref.GetReference(legacyscheme.Scheme, pod)
			if referr != nil {
				klog.Errorf("Couldn't make a ref to pod %q: '%v'", format.Pod(pod), referr)
			}
			m.recorder.Eventf(ref, v1.EventTypeWarning, events.FailedCreatePodSandBox, "Failed create pod sandbox: %v", err)
			return
		}

		klog.Infof("Created PodSandbox %q for pod %q", podSandboxID, format.Pod(pod))

		podSandboxStatus, err := m.runtimeService.PodSandboxStatus(podSandboxID)
		if err != nil {
			ref, referr := ref.GetReference(legacyscheme.Scheme, pod)
			if referr != nil {
				klog.Errorf("Couldn't make a ref to pod %q: '%v'", format.Pod(pod), referr)
			}
			m.recorder.Eventf(ref, v1.EventTypeWarning, events.FailedStatusPodSandBox, "Unable to get pod sandbox status: %v", err)
			klog.Errorf("Failed to get pod sandbox status: %v; Skipping pod %q", err, format.Pod(pod))
			result.Fail(err)
			return
		}

		// If we ever allow updating a pod from non-host-network to
		// host-network, we may use a stale IP.
		if !kubecontainer.IsHostNetworkPod(pod) {
			// Overwrite the podIP passed in the pod status, since we just started the pod sandbox.
			podIP = m.determinePodSandboxIP(pod.Namespace, pod.Name, podSandboxStatus)
			klog.Infof("Determined the ip %q for pod %q after sandbox changed", podIP, format.Pod(pod))
		}
	}

	// Get podSandboxConfig for containers to start.
	configPodSandboxResult := kubecontainer.NewSyncResult(kubecontainer.ConfigPodSandbox, podSandboxID)
	result.AddSyncResult(configPodSandboxResult)
	podSandboxConfig, err := m.generatePodSandboxConfig(pod, podContainerChanges.Attempt)
	if err != nil {
		message := fmt.Sprintf("GeneratePodSandboxConfig for pod %q failed: %v", format.Pod(pod), err)
		klog.Error(message)
		configPodSandboxResult.Fail(kubecontainer.ErrConfigPodSandbox, message)
		return
	}


	// Step 5: start the init container
	if container := podContainerChanges.NextInitContainerToStart; container != nil {
		// Start the next init container
		startContainerResult := kubecontainer.NewSyncResult(kubecontainer.StartContainer, container.Name)
		result.AddSyncResult(startContainerResult)

		// TODO doBackOff
		//isInBackOff, msg, err := m.doBackOff(pod, container, podStatus, backOff)
		//if isInBackOff {
		//	startContainerResult.Fail(err, msg)
		//	klog.V(4).Infof("Backing Off restarting init container %+v in pod %v", container, format.Pod(pod))
		//	return
		//}

		klog.Infof("Creating init container %+v in pod %v", container, format.Pod(pod))

		if msg, err := m.startContainer(podSandboxID, podSandboxConfig, container, pod, podStatus, pullSecrets, podIP); err != nil {
			startContainerResult.Fail(err, msg)
			utilruntime.HandleError(fmt.Errorf("init container start failed: %v: %s", err, msg))
			return
		}
		klog.Infof("Completed init container %q for pod %q", container.Name, format.Pod(pod))
	}

	// Step 6: start containers in podContainerChanges.ContainersToStart
	for _, idx := range podContainerChanges.ContainersToStart {
		container := &pod.Spec.Containers[idx]
		startContainerResult := kubecontainer.NewSyncResult(kubecontainer.StartContainer, container.Name)
		result.AddSyncResult(startContainerResult)

		// TODO BackOff
		//isInBackOff, msg, err := m.doBackOff(pod, container, podStatus, backOff)
		//if isInBackOff {
		//	startContainerResult.Fail(err, msg)
		//	klog.V(4).Infof("Backing Off restarting container %+v in pod %v", container, format.Pod(pod))
		//	continue
		//}

		klog.Infof("Creating container %+v in pod %v", container, format.Pod(pod))
		if msg, err := m.startContainer(podSandboxID, podSandboxConfig, container, pod, podStatus, pullSecrets, podIP); err != nil {
			startContainerResult.Fail(err, msg)
			// known errors that are logged in other places are logged at higher levels here to avoid
			// repetitive log spam
			switch {
			case err == images.ErrImagePullBackOff:
				klog.V(3).Infof("container start failed: %v: %s", err, msg)
			default:
				utilruntime.HandleError(fmt.Errorf("container start failed: %v: %s", err, msg))
			}
			continue
		}
	}


	return
}


// LegacyLogProvider gives the ability to use unsupported docker log drivers (e.g. journald)
type LegacyLogProvider interface {
	// Get the last few lines of the logs for a specific container.
	GetContainerLogTail(uid kubetypes.UID, name, namespace string, containerID kubecontainer.ContainerID) (string, error)
}

type KubeGenericRuntime interface {
	kubecontainer.Runtime

	// TODO
	// kubecontainer.StreamingRuntime
	// kubecontainer.ContainerCommandRunner
}

func NewKubeGenericRuntimeManager(
			recorder record.EventRecorder,
			runtimeService internalapi.RuntimeService,
			imageService internalapi.ImageManagerService,
			podStateProvider podStateProvider,
			legacyLogProvider LegacyLogProvider,
			seccmpProfileRoot string,
			osInterface         kubecontainer.OSInterface,
			imageBackOff *flowcontrol.Backoff,
			serializeImagePulls bool,
			imagePullQPS float32,
			imagePullBurst int,
			runtimeHelper kubecontainer.RuntimeHelper,
			) (KubeGenericRuntime, error) {
	kubeRuntimeManager := &kubeGenericRuntimeManager{
		runtimeService:		runtimeService,
		imageService:		imageService,
		logReduction:        	logreduction.NewLogReduction(identicalErrorDelay),
		legacyLogProvider: 	legacyLogProvider,
		seccompProfileRoot: 	seccmpProfileRoot,
		osInterface: 		osInterface,
		keyring:             	credentialprovider.NewDockerKeyring(),
		runtimeHelper: 		runtimeHelper,
	}

	typedVersion, err := kubeRuntimeManager.runtimeService.Version(kubeRuntimeAPIVersion)
	if err != nil {
		klog.Errorf("Get runtime version failed: %v", err)
		return nil, err
	}
	if typedVersion.Version != kubeRuntimeAPIVersion {
		klog.Errorf("Runtime api version %s is not supported, only %s is supported now",
			typedVersion.Version,
			kubeRuntimeAPIVersion)
		return nil, ErrVersionNotSupported
	}

	kubeRuntimeManager.runtimeName = typedVersion.RuntimeName
	klog.Infof("Container runtime %s initialized, version: %s, apiVersion: %s",
		typedVersion.RuntimeName,
		typedVersion.RuntimeVersion,
		typedVersion.RuntimeApiVersion)

	kubeRuntimeManager.containerGC = newContainerGC(runtimeService, podStateProvider, kubeRuntimeManager)

	kubeRuntimeManager.imagePuller = images.NewImageManager(
		kubecontainer.FilterEventRecorder(recorder),
		kubeRuntimeManager,
		imageBackOff,
		serializeImagePulls,
		imagePullQPS,
		imagePullBurst)

	return kubeRuntimeManager, nil
}

func (m *kubeGenericRuntimeManager) Type() string {
	return m.runtimeName
}


func newRuntimeVersion(version string) (*utilversion.Version, error) {
	if ver, err := utilversion.ParseSemantic(version); err == nil {
		return ver, err
	}
	return utilversion.ParseGeneric(version)
}

func (m *kubeGenericRuntimeManager) getTypedVersion() (*runtimeapi.VersionResponse, error) {
	typedVersion, err := m.runtimeService.Version(kubeRuntimeAPIVersion)
	if err != nil {
		klog.Errorf("Get remote runtime typed version failed: %v", err)
		return nil, err
	}
	return typedVersion, nil
}

// Version returns the version information of the container runtime.
func (m *kubeGenericRuntimeManager) Version() (kubecontainer.Version, error) {
	typedVersion, err := m.runtimeService.Version(kubeRuntimeAPIVersion)
	if err != nil {
		klog.Errorf("Get remote runtime version failed: %v", err)
		return nil, err
	}

	return newRuntimeVersion(typedVersion.RuntimeVersion)
}

// GetPodStatus retrieves the status of the pod, including the
// information of all containers in the pod that are visible in Runtime.
func (m *kubeGenericRuntimeManager) GetPodStatus(uid kubetypes.UID, name, namespace string) (*kubecontainer.PodStatus, error) {
	// Now we retain restart count of container as a container label. Each time a container
	// restarts, pod will read the restart count from the registered dead container, increment
	// it to get the new restart count, and then add a label with the new restart count on
	// the newly started container.
	// However, there are some limitations of this method:
	//	1. When all dead containers were garbage collected, the container status could
	//	not get the historical value and would be *inaccurate*. Fortunately, the chance
	//	is really slim.
	//	2. When working with old version containers which have no restart count label,
	//	we can only assume their restart count is 0.
	// Anyhow, we only promised "best-effort" restart count reporting, we can just ignore
	// these limitations now.
	// TODO: move this comment to SyncPod.
	podSandboxIDs, err := m.getSandboxIDByPodUID(uid, nil)
	if err != nil {
		return nil, err
	}

	podFullName := format.Pod(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       uid,
		},
	})
	klog.V(4).Infof("getSandboxIDByPodUID got sandbox IDs %q for pod %q", podSandboxIDs, podFullName)

	sandboxStatuses := make([]*runtimeapi.PodSandboxStatus, len(podSandboxIDs))
	podIP := ""
	for idx, podSandboxID := range podSandboxIDs {
		podSandboxStatus, err := m.runtimeService.PodSandboxStatus(podSandboxID)
		if err != nil {
			klog.Errorf("PodSandboxStatus of sandbox %q for pod %q error: %v", podSandboxID, podFullName, err)
			return nil, err
		}
		sandboxStatuses[idx] = podSandboxStatus

		// Only get pod IP from latest sandbox
		if idx == 0 && podSandboxStatus.State == runtimeapi.PodSandboxState_SANDBOX_READY {
			podIP = m.determinePodSandboxIP(namespace, name, podSandboxStatus)
		}
	}

	// Get statuses of all containers visible in the pod.
	containerStatuses, err := m.getPodContainerStatuses(uid, name, namespace)
	if err != nil {
		//if m.logReduction.ShouldMessageBePrinted(err.Error(), podFullName) {
		//	klog.Errorf("getPodContainerStatuses for pod %q failed: %v", podFullName, err)
		//}
		return nil, err
	}
	m.logReduction.ClearID(podFullName)

	return &kubecontainer.PodStatus{
		ID:                uid,
		Name:              name,
		Namespace:         namespace,
		IP:                podIP,
		SandboxStatuses:   sandboxStatuses,
		ContainerStatuses: containerStatuses,
	}, nil
}

// determinePodSandboxIP determines the IP address of the given pod sandbox.
func (m *kubeGenericRuntimeManager) determinePodSandboxIP(podNamespace, podName string, podSandbox *runtimeapi.PodSandboxStatus) string {
	if podSandbox.Network == nil {
		klog.Warningf("Pod Sandbox status doesn't have network information, cannot report IP")
		return ""
	}
	ip := podSandbox.Network.Ip
	if len(ip) != 0 && net.ParseIP(ip) == nil {
		// ip could be an empty string if runtime is not responsible for the
		// IP (e.g., host networking).
		klog.Warningf("Pod Sandbox reported an unparseable IP %v", ip)
		return ""
	}
	return ip
}


func (m *kubeGenericRuntimeManager) GetPods(all bool) ([]*kubecontainer.Pod, error) {
	pods := make(map[kubetypes.UID]*kubecontainer.Pod)

	sandboxes, err := m.getKubeletSandboxes(all)
	if err != nil {
		return nil, err
	}

	for i := range sandboxes {
		s := sandboxes[i]
		if s.Metadata == nil {
			klog.Infof("Sandbox does not have metadata: %+v", s)
			continue
		}

		podUID := kubetypes.UID(s.Metadata.Uid)
		if _, ok := pods[podUID]; !ok {
			pods[podUID] = &kubecontainer.Pod{
				ID: 		podUID,
				Name: 		s.Metadata.Name,
				Namespace:	s.Metadata.Namespace,
			}
		}
		p := pods[podUID]
		converted, err := m.sandboxToKubeContainer(s)
		if err != nil {
			klog.V(4).Infof("Convert %q sandbox %v of pod %q failed: %v", m.runtimeName, s, podUID, err)
			continue
		}
		p.Sandboxes = append(p.Sandboxes, converted)
	}

	containers, err := m.getKubeletContainers(all)
	if err != nil {
		return nil, err
	}

	for i := range containers {
		c := containers[i]
		if c.Metadata == nil {
			klog.Infof("Container does not have metadata: %+v", c)
			continue
		}

		labelledInfo := getContainerInfoFromLabels(c.Labels)
		pod, found := pods[labelledInfo.PodUID]
		if !found {
			pod = &kubecontainer.Pod{
				ID: 		labelledInfo.PodUID,
				Name:		labelledInfo.PodName,
				Namespace: 	labelledInfo.PodNamespace,
			}
			pods[labelledInfo.PodUID] = pod
		}

		converted, err := m.toKubeContainer(c)
		if err != nil {
			klog.V(4).Infof("Convert %s container %v of pod %q failed: %v", m.runtimeName, c, labelledInfo.PodUID, err)
			continue
		}

		pod.Containers = append(pod.Containers, converted)
	}

	var result []*kubecontainer.Pod
	for _, pod := range pods {
		result = append(result, pod)
	}

	return result, nil

}

// GarbageCollect removes dead containers using the specified container gc policy.
func (m *kubeGenericRuntimeManager) GarbageCollect(gcPolicy kubecontainer.ContainerGCPolicy, allSourcesReady bool, evictNonDeletedPods bool) error {
	return m.containerGC.GarbageCollect(gcPolicy, allSourcesReady, evictNonDeletedPods)
}
