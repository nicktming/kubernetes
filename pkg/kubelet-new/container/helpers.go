package container

import (
	"k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	hashutil "k8s.io/kubernetes/pkg/util/hash"
	"hash/fnv"
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

// HashContainer returns the hash of the container. It is used to compare
// the running container with its desired spec.
func HashContainer(container *v1.Container) uint64 {
	hash := fnv.New32a()
	hashutil.DeepHashObject(hash, *container)
	return uint64(hash.Sum32())
}

