package kubelet_tming


import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/volume"
)

// ListVolumesForPod returns a map of the mounted volumes for the given pod.
// The key in the map is the OuterVolumeSpecName (i.e. pod.Spec.Volumes[x].Name)
func (kl *Kubelet) ListVolumesForPod(podUID types.UID) (map[string]volume.Volume, bool) {
	volumesToReturn := make(map[string]volume.Volume)
	//podVolumes := kl.volumeManager.GetMountedVolumesForPod(
	//	volumetypes.UniquePodName(podUID))
	//for outerVolumeSpecName, volume := range podVolumes {
	//	// TODO: volume.Mounter could be nil if volume object is recovered
	//	// from reconciler's sync state process. PR 33616 will fix this problem
	//	// to create Mounter object when recovering volume state.
	//	if volume.Mounter == nil {
	//		continue
	//	}
	//	volumesToReturn[outerVolumeSpecName] = volume.Mounter
	//}

	return volumesToReturn, len(volumesToReturn) > 0
}
