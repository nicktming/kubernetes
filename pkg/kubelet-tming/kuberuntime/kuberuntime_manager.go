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
)


const (
	kubeRuntimeAPIVersion	= "0.1.0"

	// How frequently to report identical errors
	identicalErrorDelay = 1 * time.Minute
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
			runtimeService internalapi.RuntimeService,
			imageService internalapi.ImageManagerService,
			podStateProvider podStateProvider,
			legacyLogProvider LegacyLogProvider,
			) (KubeGenericRuntime, error) {
	kubeRuntimeManager := &kubeGenericRuntimeManager{
		runtimeService:		runtimeService,
		imageService:		imageService,
		logReduction:        	logreduction.NewLogReduction(identicalErrorDelay),
		legacyLogProvider: 	legacyLogProvider,
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
