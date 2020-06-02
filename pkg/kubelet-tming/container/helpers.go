package container


import (
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

func SandboxToContainerState(state runtimeapi.PodSandboxState) ContainerState {
	switch state {
	case runtimeapi.PodSandboxState_SANDBOX_READY:
		return ContainerStateRunning
	case runtimeapi.PodSandboxState_SANDBOX_NOTREADY:
		return ContainerStateExited
	}
	return ContainerStateUnknown
}


// Pod must not be nil.
func IsHostNetworkPod(pod *v1.Pod) bool {
	return pod.Spec.HostNetwork
}
