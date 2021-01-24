package container

import (
	"k8s.io/api/core/v1"
)

// Pod must not be nil.
func IsHostNetworkPod(pod *v1.Pod) bool {
	return pod.Spec.HostNetwork
}

