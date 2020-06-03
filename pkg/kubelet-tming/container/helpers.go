package container


import (
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog"
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
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

// ShouldContainerBeRestarted checks whether a container needs to be restarted.
// TODO(yifan): Think about how to refactor this.
func ShouldContainerBeRestarted(container *v1.Container, pod *v1.Pod, podStatus *PodStatus) bool {
	// Get latest container status.
	status := podStatus.FindContainerStatusByName(container.Name)
	// If the container was never started before, we should start it.
	// NOTE(random-liu): If all historical containers were GC'd, we'll also return true here.
	if status == nil {
		return true
	}
	// Check whether container is running
	if status.State == ContainerStateRunning {
		return false
	}
	// Always restart container in the unknown, or in the created state.
	if status.State == ContainerStateUnknown || status.State == ContainerStateCreated {
		return true
	}
	// Check RestartPolicy for dead container
	if pod.Spec.RestartPolicy == v1.RestartPolicyNever {
		klog.V(4).Infof("Already ran container %q of pod %q, do nothing", container.Name, format.Pod(pod))
		return false
	}
	if pod.Spec.RestartPolicy == v1.RestartPolicyOnFailure {
		// Check the exit code.
		if status.ExitCode == 0 {
			klog.V(4).Infof("Already successfully ran container %q of pod %q, do nothing", container.Name, format.Pod(pod))
			return false
		}
	}
	return true
}
