package container

import (
	"k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

// Pod must not be nil.
func IsHostNetworkPod(pod *v1.Pod) bool {
	return pod.Spec.HostNetwork
}

func SandboxToContainerState(state runtimeapi.PodSandboxState) ContainerState {
	switch state {
	case runtimeapi.PodSandboxState_SANDBOX_READY:
		return ContainerStateRunning
	case runtimeapi.PodSandboxState_SANDBOX_NOTREADY:
		return ContainerStateExited
	}
	return ContainerStateUnknown
}
