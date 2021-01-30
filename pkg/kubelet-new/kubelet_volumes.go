package kubelet_new

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
)

func (kl *Kubelet) podVolumesExist(podUID types.UID) bool {
	// TODO mounted volumes
	volumePaths, err := kl.getMountedVolumePathListFromDisk(podUID)
	if err != nil {
		klog.Errorf("pod %q found, but error %v occurred during checking mounted volumes from disk", podUID, err)
		return true
	}
	if len(volumePaths) > 0 {
		klog.Infof("pod %q found, but volumes are still mounted on disk %v", podUID, volumePaths)
		return true
	}
	return false
}



