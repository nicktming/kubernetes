package populator


import (
	"sync"
	"time"
	"k8s.io/api/core/v1"

	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/kubelet-tming/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	"k8s.io/kubernetes/pkg/kubelet-tming/pod"
	"k8s.io/kubernetes/pkg/kubelet-tming/status"
	"k8s.io/kubernetes/pkg/kubelet-tming/volumemanager/cache"
	volumetypes "k8s.io/kubernetes/pkg/volume/util/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/pkg/volume/util"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
)
type DesiredStateOfWorldPopulator interface {
	Run(sourcesReady config.SourcesReady, stopCh <-chan struct{})
}

// NewDesiredStateOfWorldPopulator returns a new instance of
// DesiredStateOfWorldPopulator.
//
// kubeClient - used to fetch PV and PVC objects from the API server
// loopSleepDuration - the amount of time the populator loop sleeps between
//     successive executions
// podManager - the kubelet podManager that is the source of truth for the pods
//     that exist on this host
// desiredStateOfWorld - the cache to populate
func NewDesiredStateOfWorldPopulator(
	kubeClient clientset.Interface,
	loopSleepDuration time.Duration,
	getPodStatusRetryDuration time.Duration,
	podManager pod.Manager,
	podStatusProvider status.PodStatusProvider,
	desiredStateOfWorld cache.DesiredStateOfWorld,
	actualStateOfWorld cache.ActualStateOfWorld,
	kubeContainerRuntime kubecontainer.Runtime,
	keepTerminatedPodVolumes bool) DesiredStateOfWorldPopulator {
	return &desiredStateOfWorldPopulator{
		kubeClient:                kubeClient,
		loopSleepDuration:         loopSleepDuration,
		getPodStatusRetryDuration: getPodStatusRetryDuration,
		podManager:                podManager,
		podStatusProvider:         podStatusProvider,
		desiredStateOfWorld:       desiredStateOfWorld,
		actualStateOfWorld:        actualStateOfWorld,
		pods: processedPods{
			processedPods: make(map[volumetypes.UniquePodName]bool)},
		kubeContainerRuntime:     kubeContainerRuntime,
		keepTerminatedPodVolumes: keepTerminatedPodVolumes,
		hasAddedPods:             false,
		hasAddedPodsLock:         sync.RWMutex{},
	}
}

type desiredStateOfWorldPopulator struct {
	kubeClient                clientset.Interface
	loopSleepDuration         time.Duration
	getPodStatusRetryDuration time.Duration
	podManager                pod.Manager
	podStatusProvider         status.PodStatusProvider
	desiredStateOfWorld       cache.DesiredStateOfWorld
	actualStateOfWorld        cache.ActualStateOfWorld
	pods                      processedPods
	kubeContainerRuntime      kubecontainer.Runtime
	timeOfLastGetPodStatus    time.Time
	keepTerminatedPodVolumes  bool
	hasAddedPods              bool
	hasAddedPodsLock          sync.RWMutex
}

type processedPods struct {
	processedPods map[volumetypes.UniquePodName]bool
	sync.RWMutex
}

func (dswp *desiredStateOfWorldPopulator) Run(sourcesReady config.SourcesReady, stopCh <- chan struct{}) {
	// Wait for the completion of a loop that started after sources are all ready, then set hasAddedPods accordingly
	wait.PollUntil(dswp.loopSleepDuration, func() (bool, error) {
		done := sourcesReady.AllReady()
		dswp.populatorLoop()
		return done, nil
	}, stopCh)

	dswp.hasAddedPodsLock.Lock()
	dswp.hasAddedPods = true
	dswp.hasAddedPodsLock.Unlock()

	wait.Until(dswp.populatorLoop, dswp.loopSleepDuration, stopCh)
}


func (dswp *desiredStateOfWorldPopulator) populatorLoop() {
	dswp.findAndAddNewPods()
	// findAndRemoveDeletedPods() calls out to the container runtime to
	// determine if the containers for a given pod are terminated. This is
	// an expensive operation, therefore we limit the rate that
	// findAndRemoveDeletedPods() is called independently of the main
	// populator loop.
	if time.Since(dswp.timeOfLastGetPodStatus) < dswp.getPodStatusRetryDuration {
		klog.V(5).Infof(
			"Skipping findAndRemoveDeletedPods(). Not permitted until %v (getPodStatusRetryDuration %v).",
			dswp.timeOfLastGetPodStatus.Add(dswp.getPodStatusRetryDuration),
			dswp.getPodStatusRetryDuration)

		return
	}
	dswp.findAndRemoveDeletedPods()
}

// Iterate through all pods in desired state of world, and remove if they no
// longer exist
func (dswp *desiredStateOfWorldPopulator) findAndRemoveDeletedPods() {

}

func (dswp *desiredStateOfWorldPopulator) isPodTerminated(pod *v1.Pod) bool {
	podStatus, found := dswp.podStatusProvider.GetPodStatus(pod.UID)
	if !found {
		podStatus = pod.Status
	}
	return util.IsPodTerminated(pod, podStatus)
}

// podPreviouslyProcessed returns true if the volumes for this pod have already
// been processed by the populator
func (dswp *desiredStateOfWorldPopulator) podPreviouslyProcessed(
podName volumetypes.UniquePodName) bool {
	dswp.pods.RLock()
	defer dswp.pods.RUnlock()

	_, exists := dswp.pods.processedPods[podName]
	return exists
}

// markPodProcessed records that the volumes for the specified pod have been
// processed by the populator
func (dswp *desiredStateOfWorldPopulator) markPodProcessed(
podName volumetypes.UniquePodName) {
	dswp.pods.Lock()
	defer dswp.pods.Unlock()

	dswp.pods.processedPods[podName] = true
}

// deleteProcessedPod removes the specified pod from processedPods
func (dswp *desiredStateOfWorldPopulator) deleteProcessedPod(
podName volumetypes.UniquePodName) {
	dswp.pods.Lock()
	defer dswp.pods.Unlock()

	delete(dswp.pods.processedPods, podName)
}


func (dswp *desiredStateOfWorldPopulator) findAndAddNewPods() {
	// Map unique pod name to outer volume name to MountedVolume.
	mountedVolumesForPod := make(map[volumetypes.UniquePodName]map[string]cache.MountedVolume)
	//if utilfeature.DefaultFeatureGate.Enabled(features.ExpandInUsePersistentVolumes) {
	//	for _, mountedVolume := range dswp.actualStateOfWorld.GetMountedVolumes() {
	//		mountedVolumes, exist := mountedVolumesForPod[mountedVolume.PodName]
	//		if !exist {
	//			mountedVolumes = make(map[string]cache.MountedVolume)
	//			mountedVolumesForPod[mountedVolume.PodName] = mountedVolumes
	//		}
	//		mountedVolumes[mountedVolume.OuterVolumeSpecName] = mountedVolume
	//	}
	//}

	processedVolumesForFSResize := sets.NewString()
	for _, pod := range dswp.podManager.GetPods() {
		if dswp.isPodTerminated(pod) {
			// Do not (re)add volumes for terminated pods
			continue
		}
		dswp.processPodVolumes(pod, mountedVolumesForPod, processedVolumesForFSResize)
	}
}

func (dswp *desiredStateOfWorldPopulator) makeVolumeMap(containers []v1.Container) (map[string]bool, map[string]bool) {
	volumeDevicesMap := make(map[string]bool)
	volumeMountsMap  := make(map[string]bool)
	for _, container := range containers {
		if container.VolumeMounts != nil {
			for _, mount := range container.VolumeMounts {
				volumeMountsMap[mount.Name] = true
			}
		}

		// TODO: remove feature gate check after no longer needed
		//if utilfeature.DefaultFeatureGate.Enabled(features.BlockVolume) &&
		//	container.VolumeDevices != nil {
		//	for _, device := range container.VolumeDevices {
		//		volumeDevicesMap[device.Name] = true
		//	}
		//}
	}

	return volumeMountsMap, volumeDevicesMap
}


func (dswp *desiredStateOfWorldPopulator) createVolumeSpec(
	podVolume v1.Volume, podName string, podNamespace string, mountsMap map[string]bool, devicesMap map[string]bool) (*v1.PersistentVolumeClaim, *volume.Spec, string, error) {
	if pvcSource := podVolume.VolumeSource.PersistentVolumeClaim; pvcSource != nil {

	}

	// Do not return the original volume object, since the source could mutate it
	clonedPodVolume := podVolume.DeepCopy()

	return nil, volume.NewSpecFromVolume(clonedPodVolume), "", nil
}

// processPodVolumes processes the volumes in the given pod and adds them to the
// desired state of the world.
func (dswp *desiredStateOfWorldPopulator) processPodVolumes(pod *v1.Pod,
			mountedVolumesForPod map[volumetypes.UniquePodName]map[string]cache.MountedVolume,
			processedVolumesForFSResize sets.String) {
	if pod == nil {
		return
	}
	uniquePodName := util.GetUniquePodName(pod)
	if dswp.podPreviouslyProcessed(uniquePodName) {
		return
	}

	allVolumesAdded := true
	mountsMap, devicesMap := dswp.makeVolumeMap(pod.Spec.Containers)

	// Process volume spec for each volume defined in pod
	for _, podVolume := range pod.Spec.Volumes {
		_, volumeSpec, volumeGidValue, err :=
			dswp.createVolumeSpec(podVolume, pod.Name, pod.Namespace, mountsMap, devicesMap)
		if err != nil {
			klog.Errorf(
				"Error processing volume %q for pod %q: %v",
				podVolume.Name,
				format.Pod(pod),
				err)
			allVolumesAdded = false
			continue
		}

		// Add volume to desired state of world
		_, err = dswp.desiredStateOfWorld.AddPodToVolume(
			uniquePodName, pod, volumeSpec, podVolume.Name, volumeGidValue)
		if err != nil {
			klog.Errorf(
				"Failed to add volume %q (specName: %q) for pod %q to desiredStateOfWorld. err=%v",
				podVolume.Name,
				volumeSpec.Name(),
				uniquePodName,
				err)
			allVolumesAdded = false
		}

		klog.V(4).Infof(
			"Added volume %q (volSpec=%q) for pod %q to desired state.",
			podVolume.Name,
			volumeSpec.Name(),
			uniquePodName)

		//if utilfeature.DefaultFeatureGate.Enabled(features.ExpandInUsePersistentVolumes) {
		//	dswp.checkVolumeFSResize(pod, podVolume, pvc, volumeSpec,
		//		uniquePodName, mountedVolumesForPod, processedVolumesForFSResize)
		//}

	}
	// some of the volume additions may have failed, should not mark this pod as fully processed
	if allVolumesAdded {
		dswp.markPodProcessed(uniquePodName)
		// New pod has been synced. Re-mount all volumes that need it (e.g. DownwardAPI)
		dswp.actualStateOfWorld
	}

}





































