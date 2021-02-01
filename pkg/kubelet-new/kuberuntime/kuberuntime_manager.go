package kuberuntime

import (
	internalapi "k8s.io/cri-api/pkg/apis"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/api/core/v1"
	//"k8s.io/client-go/util/flowcontrol"
	"errors"
	"time"
	"os"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	"k8s.io/kubernetes/pkg/api/ref"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/kubelet/events"
	"encoding/json"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	kubetypes "k8s.io/apimachinery/pkg/types"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"fmt"
)

const (
	// The api version of kubelet runtime api
	kubeRuntimeAPIVersion = "0.1.0"
	// The root directory for pod logs
	podLogsRootDirectory = "/var/log/pods"
	// A minimal shutdown window for avoiding unnecessary SIGKILLs
	minimumGracePeriodInSeconds = 2

	// The expiration time of version cache.
	versionCacheTTL = 60 * time.Second
	// How frequently to report identical errors
	identicalErrorDelay = 1 * time.Minute
)

var (
	// ErrVersionNotSupported is returned when the api version of runtime interface is not supported
	ErrVersionNotSupported = errors.New("Runtime api version is not supported")
)

type kubeGenericRuntimeManager struct {
	runtimeName string

	runtimeService internalapi.RuntimeService
	imageService internalapi.ImageManagerService
	osInterface kubecontainer.OSInterface
	recorder            record.EventRecorder
}

// KubeGenericRuntime is a interface contains interfaces for container runtime and command.
type KubeGenericRuntime interface {
	kubecontainer.Runtime
	//kubecontainer.StreamingRuntime
	//kubecontainer.ContainerCommandRunner
}

func NewKubeGenericRuntimeManager(
	recorder	record.EventRecorder,
	osInterface 	kubecontainer.OSInterface,
	runtimeService 	internalapi.RuntimeService,
	imageService 	internalapi.ImageManagerService) (KubeGenericRuntime, error) {

	kubeRuntimeManager := &kubeGenericRuntimeManager{
		recorder: 		recorder,
		runtimeService:      	runtimeService,
		imageService:        	imageService,
		osInterface: 		osInterface,

	}

	if _, err := osInterface.Stat(podLogsRootDirectory); os.IsNotExist(err) {
		if err := osInterface.MkdirAll(podLogsRootDirectory, 0755); err != nil {
			klog.Errorf("Failed to create directory %q: %v", podLogsRootDirectory, err)
		}
	}

	return kubeRuntimeManager, nil
}

// podActions keeps information what to do for a pod.
type podActions struct {
	SandboxID string

	CreateSandbox bool

	ContainersToStart []int

	Attempt uint32
}

// computePodActions check whether the pod spec has changed and returns the changes if true
func (m *kubeGenericRuntimeManager) computePodActions(pod *v1.Pod, podStatus *kubecontainer.PodStatus) podActions {
	pa := podActions {
		ContainersToStart: 	make([]int, 0),
	}
	if podStatus == nil {
		podStatus = &kubecontainer.PodStatus{}
	}
	if len(podStatus.SandboxStatuses) == 0 {
		pa.CreateSandbox = true
	} else {
		sandboxStatus := podStatus.SandboxStatuses[0]
		if sandboxStatus.State == runtimeapi.PodSandboxState_SANDBOX_NOTREADY {
			pa.CreateSandbox = true
			pa.Attempt = sandboxStatus.Metadata.Attempt + 1
		} else {
			pa.SandboxID = sandboxStatus.Id
			pa.Attempt  =  sandboxStatus.Metadata.Attempt
		}
	}
	for i := range pod.Spec.Containers {
		container := pod.Spec.Containers[i]
		containerStatus := podStatus.GetContainerStatusFromPodStatus(container.Name)

		if containerStatus == nil || containerStatus.State != kubecontainer.ContainerStateRunning {
			pa.ContainersToStart = append(pa.ContainersToStart, i)
		}
	}
	return pa
}

// SyncPod syncs the running pod into the desired pod by executing following steps:
//
//  1. Compute sandbox and container changes.
//  2. Kill pod sandbox if necessary.
//  3. Kill any containers that should not be running.
//  4. Create sandbox if necessary.
//  5. Create init containers.
//  6. Create normal containers.
func (m *kubeGenericRuntimeManager) SyncPod(pod *v1.Pod,
			podStatus *kubecontainer.PodStatus,
			//pullSecrets []v1.Secret,
			//backOff *flowcontrol.Backoff,
			) (result kubecontainer.PodSyncResult) {

	podContainerChanges := m.computePodActions(pod, podStatus)

	pretty_podContainerChanges, _ := json.MarshalIndent(podContainerChanges, "", "\t")
	pretty_podStatus, _ := json.MarshalIndent(podStatus, "", "\t")
	fmt.Printf("poduid: %v\npodStatus: %v\ncompute pod actions: %v\n", pod.UID, string(pretty_podStatus), string(pretty_podContainerChanges))

	podSandboxID := podContainerChanges.SandboxID
	var podIP string

	if podContainerChanges.CreateSandbox {
		var msg string
		var err error

		klog.Infof("Creating sandbox for pod %q", format.Pod(pod))
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

		//pretty_podSandboxStatus, _ := json.MarshalIndent(podSandboxStatus, "", "\t")
		//fmt.Printf("pretty sand box status: %v\n", string(pretty_podSandboxStatus))

		// If we ever allow updating a pod from non-host-network to
		// host-network, we may use a stale IP.
		if !kubecontainer.IsHostNetworkPod(pod) {
			// Overwrite the podIP passed in the pod status, since we just started the pod sandbox.
			podIP = m.determinePodSandboxIP(pod.Namespace, pod.Name, podSandboxStatus)
			klog.Infof("Determined the ip %q for pod %q after sandbox changed", podIP, format.Pod(pod))
		}
	}
	podSandboxConfig, _ := m.generatePodSandboxConfig(pod, podContainerChanges.Attempt)
	if len(podContainerChanges.ContainersToStart) > 0 {
		for _, idx := range podContainerChanges.ContainersToStart {
			container := &pod.Spec.Containers[idx]
			startContainerResult := kubecontainer.NewSyncResult(kubecontainer.StartContainer, container.Name)
			result.AddSyncResult(startContainerResult)

			klog.Infof("Creating container %+v in pod %v", container, format.Pod(pod))
			if msg, err := m.startContainer(podSandboxID, podSandboxConfig, container, pod, podIP, podStatus); err != nil {
				startContainerResult.Fail(err, msg)
				// known errors that are logged in other places are logged at higher levels here to avoid
				// repetitive log spam
				switch {
				//case err == images.ErrImagePullBackOff:
				//	klog.V(3).Infof("container start failed: %v: %s", err, msg)
				default:
					utilruntime.HandleError(fmt.Errorf("container start failed: %v: %s", err, msg))
				}
				continue
			}
		}
	}
	return
}


func (m *kubeGenericRuntimeManager) GetPods(all bool) ([]*kubecontainer.Pod, error) {
	pods := make(map[kubetypes.UID]*kubecontainer.Pod)

	sandboxes, err := m.getKubeletSandboxes(all)
	if err != nil {
		return nil, err
	}

	for i := range sandboxes {
		s := sandboxes[i]
		//pretty_sandbox, _ := json.MarshalIndent(s, "", "\t")
		//fmt.Printf("kubeGenericRuntimeManager GetPods pretty sandbox: %s\n, s.metadata: %v, s.Metadata == nil (%v)\n",
		//	string(pretty_sandbox), s.Metadata, (s.Metadata == nil))
		if s.Metadata == nil {
			klog.Infof("Sandbox does not have metadata: %+v", s)
			continue
		}
		podUID := kubetypes.UID(s.Metadata.Uid)
		if _, ok := pods[podUID]; !ok {
			pods[podUID] = &kubecontainer.Pod{
				ID: 		podUID,
				Name: 		s.Metadata.Name,
				Namespace:      s.Metadata.Namespace,
			}
		}
		pod := pods[podUID]
		converted, err := m.sandboxToKubeContainer(s)
		if err != nil {
			klog.V(4).Infof("Convert %q sandbox %v of pod %q failed: %v", m.runtimeName, s, podUID, err)
			continue
		}
		pod.Sandboxes = append(pod.Sandboxes, converted)
	}

	containers, err := m.getKubeletContainers(all)
	if err != nil {
		return nil, err
	}
	for i := range containers {
		c := containers[i]
		if c.Metadata == nil {
			klog.Infof("container does not have metadata: %+v", c)
			continue
		}
		labelledInfo := getContainerInfoFromLabels(c.Labels)
		pod, found := pods[labelledInfo.PodUID]
		if !found {
			pod = &kubecontainer.Pod{
				ID:        labelledInfo.PodUID,
				Name:      labelledInfo.PodName,
				Namespace: labelledInfo.PodNamespace,
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

func (m *kubeGenericRuntimeManager) GetPodStatus(uid kubetypes.UID, name, namespace string) (*kubecontainer.PodStatus, error) {
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
	//m.logReduction.ClearID(podFullName)

	return &kubecontainer.PodStatus{
		ID:                uid,
		Name:              name,
		Namespace:         namespace,
		IP:                podIP,
		SandboxStatuses:   sandboxStatuses,
		ContainerStatuses: containerStatuses,
	}, nil
}


// KillPod kills all the containers of a pod. Pod may be nil, running pod must not be.
// gracePeriodOverride if specified allows the caller to override the pod default grace period.
// only hard kill paths are allowed to specify a gracePeriodOverride in the kubelet in order to not corrupt user data.
// it is useful when doing SIGKILL for hard eviction scenarios, or max grace period during soft eviction scenarios.
func (m *kubeGenericRuntimeManager) KillPod(pod *v1.Pod, runningPod kubecontainer.Pod, gracePeriodOverride *int64) error {
	err := m.killPodWithSyncResult(pod, runningPod, gracePeriodOverride)
	return err.Error()
}

// killPodWithSyncResult kills a runningPod and returns SyncResult.
// Note: The pod passed in could be *nil* when kubelet restarted.
func (m *kubeGenericRuntimeManager) killPodWithSyncResult(pod *v1.Pod, runningPod kubecontainer.Pod, gracePeriodOverride *int64) (result kubecontainer.PodSyncResult) {
	killContainerResults := m.killContainersWithSyncResult(pod, runningPod, gracePeriodOverride)
	for _, containerResult := range killContainerResults {
		result.AddSyncResult(containerResult)
	}

	// stop sandbox, the sandbox will be removed in GarbageCollect
	killSandboxResult := kubecontainer.NewSyncResult(kubecontainer.KillPodSandbox, runningPod.ID)
	result.AddSyncResult(killSandboxResult)
	// Stop all sandboxes belongs to same pod
	for _, podSandbox := range runningPod.Sandboxes {
		if err := m.runtimeService.StopPodSandbox(podSandbox.ID.ID); err != nil {
			killSandboxResult.Fail(kubecontainer.ErrKillPodSandbox, err.Error())
			klog.Errorf("Failed to stop sandbox %q", podSandbox.ID)
		}
	}

	return
}


// Type returns the type of the container runtime.
func (m *kubeGenericRuntimeManager) Type() string {
	return m.runtimeName
}








































