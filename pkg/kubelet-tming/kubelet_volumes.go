package kubelet_tming


import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/klog"

	v1 "k8s.io/api/core/v1"
	"fmt"
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


// newVolumeMounterFromPlugins attempts to find a plugin by volume spec, pod
// and volume options and then creates a Mounter.
// Returns a valid mounter or an error.
func (kl *Kubelet) newVolumeMounterFromPlugins(spec *volume.Spec, pod *v1.Pod, opts volume.VolumeOptions) (volume.Mounter, error) {
	plugin, err := kl.volumePluginMgr.FindPluginBySpec(spec)
	if err != nil {
		return nil, fmt.Errorf("can't use volume plugins for %s: %v", spec.Name(), err)
	}
	physicalMounter, err := plugin.NewMounter(spec, pod, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate mounter for volume: %s using plugin: %s with a root cause: %v", spec.Name(), plugin.GetPluginName(), err)
	}
	klog.V(10).Infof("Using volume plugin %q to mount %s", plugin.GetPluginName(), spec.Name())
	return physicalMounter, nil
}