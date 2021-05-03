package cache

import (
	"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
)

// MountedVolume represents a volume that has successfully been mounted to a pod.
type MountedVolume struct {
	operationexecutor.MountedVolume
}




