package kuberuntime

import (
	internalapi "k8s.io/cri-api/pkg/apis"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
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
	"gopkg.in/square/go-jose.v2/json"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
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


// SyncPod syncs the running pod into the desired pod by executing following steps:
//
//  1. Compute sandbox and container changes.
//  2. Kill pod sandbox if necessary.
//  3. Kill any containers that should not be running.
//  4. Create sandbox if necessary.
//  5. Create init containers.
//  6. Create normal containers.
func (m *kubeGenericRuntimeManager) SyncPod(pod *v1.Pod,
			//podStatus *kubecontainer.PodStatus,
			//pullSecrets []v1.Secret,
			//backOff *flowcontrol.Backoff,
			) (result kubecontainer.PodSyncResult) {

	var podSandboxID string
	var podIP string


	{
		var msg string
		var err error

		klog.Infof("Creating sandbox for pod %q", format.Pod(pod))
		createSandboxResult := kubecontainer.NewSyncResult(kubecontainer.CreatePodSandbox, format.Pod(pod))
		result.AddSyncResult(createSandboxResult)
		podSandboxID, msg, err := m.createPodSandbox(pod, 0)
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

		pretty_podSandboxStatus, _ := json.MarshalIndent(podSandboxStatus, "", "\t")
		fmt.Printf("pretty sand box status: %v\n", string(pretty_podSandboxStatus))

		// If we ever allow updating a pod from non-host-network to
		// host-network, we may use a stale IP.
		if !kubecontainer.IsHostNetworkPod(pod) {
			// Overwrite the podIP passed in the pod status, since we just started the pod sandbox.
			podIP = m.determinePodSandboxIP(pod.Namespace, pod.Name, podSandboxStatus)
			klog.Infof("Determined the ip %q for pod %q after sandbox changed", podIP, format.Pod(pod))
		}
	}

	podSandboxConfig, _ := m.generatePodSandboxConfig(pod, 0)
	{
		for _, idx := range pod.Spec.Containers {
			container := &pod.Spec.Containers[idx]
			startContainerResult := kubecontainer.NewSyncResult(kubecontainer.StartContainer, container.Name)
			result.AddSyncResult(startContainerResult)

			klog.Infof("Creating container %+v in pod %v", container, format.Pod(pod))
			if msg, err := m.startContainer(podSandboxID, podSandboxConfig, container, pod, podIP); err != nil {
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















































