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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"fmt"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/apimachinery/pkg/types"
)


type DesiredStateOfWorldPopulator interface {
	Run(sourcesReady config.SourcesReady, stopCh <-chan struct{})


	// ReprocessPod removes the specified pod from the list of processedPods
	// (if it exists) forcing it to be reprocessed. This is required to enable
	// remounting volumes on pod updates (volumes like Downward API volumes
	// depend on this behavior to ensure volume content is updated).
	ReprocessPod(podName volumetypes.UniquePodName)

	// HasAddedPods returns whether the populator has looped through the list
	// of active pods and added them to the desired state of the world cache,
	// at a time after sources are all ready, at least once. It does not
	// return true before sources are all ready because before then, there is
	// a chance many or all pods are missing from the list of active pods and
	// so few to none will have been added.
	HasAddedPods() bool
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

func (dswp *desiredStateOfWorldPopulator) HasAddedPods() bool {
	dswp.hasAddedPodsLock.RLock()
	defer dswp.hasAddedPodsLock.RUnlock()
	return dswp.hasAddedPods
}

func (dswp *desiredStateOfWorldPopulator) ReprocessPod(
	podName volumetypes.UniquePodName) {
	dswp.deleteProcessedPod(podName)
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

// getPVCExtractPV fetches the PVC object with the given namespace and name from
// the API server, checks whether PVC is being deleted, extracts the name of the PV
// it is pointing to and returns it.
// An error is returned if the PVC object's phase is not "Bound".
func (dswp *desiredStateOfWorldPopulator) getPVCExtractPV(namespace string, claimName string) (*v1.PersistentVolumeClaim, error) {
	pvc, err :=
		dswp.kubeClient.CoreV1().PersistentVolumeClaims(namespace).Get(claimName, metav1.GetOptions{})

	if err != nil || pvc == nil {
		return nil, fmt.Errorf(
			"failed to fetch PVC %s/%s from API server. err=%v",
			namespace,
			claimName,
			err)
	}
	if utilfeature.DefaultFeatureGate.Enabled(features.StorageObjectInUseProtection) {
		// Pods that uses a PVC that is being deleted must not be started.
		//
		// In case an old kubelet is running without this check or some kubelets
		// have this feature disabled, the worst that can happen is that such
		// pod is scheduled. This was the default behavior in 1.8 and earlier
		// and users should not be that surprised.
		// It should happen only in very rare case when scheduler schedules
		// a pod and user deletes a PVC that's used by it at the same time.
		if pvc.ObjectMeta.DeletionTimestamp != nil {
			return nil, fmt.Errorf(
				"can't start pod because PVC %s/%s is being deleted",
				namespace,
				claimName)
		}
	}

	if pvc.Status.Phase != v1.ClaimBound || pvc.Spec.VolumeName == "" {
		return nil, fmt.Errorf(
			"PVC %s/%s has non-bound phase (%q) or empty pvc.Spec.VolumeName (%q)",
			namespace,
			claimName,
			pvc.Status.Phase,
			pvc.Spec.VolumeName)
	}

	return pvc, nil
}

// getPVSpec fetches the PV object with the given name from the API server
// and returns a volume.Spec representing it.
// An error is returned if the call to fetch the PV object fails.
func (dswp *desiredStateOfWorldPopulator) getPVSpec(name string, pvcReadOnly bool, expectedClaimUID types.UID) (*volume.Spec, string, error) {
	pv, err := dswp.kubeClient.CoreV1().PersistentVolumes().Get(name, metav1.GetOptions{})
	if err != nil || pv == nil {
		return nil, "", fmt.Errorf(
			"failed to fetch PV %q from API server. err=%v", name, err)
	}

	if pv.Spec.ClaimRef == nil {
		return nil, "", fmt.Errorf(
			"found PV object %q but it has a nil pv.Spec.ClaimRef indicating it is not yet bound to the claim",
			name)
	}

	if pv.Spec.ClaimRef.UID != expectedClaimUID {
		return nil, "", fmt.Errorf(
			"found PV object %q but its pv.Spec.ClaimRef.UID (%q) does not point to claim.UID (%q)",
			name,
			pv.Spec.ClaimRef.UID,
			expectedClaimUID)
	}

	volumeGidValue := getPVVolumeGidAnnotationValue(pv)
	return volume.NewSpecFromPersistentVolume(pv, pvcReadOnly), volumeGidValue, nil
}

func getPVVolumeGidAnnotationValue(pv *v1.PersistentVolume) string {
	if volumeGid, ok := pv.Annotations[util.VolumeGidAnnotationKey]; ok {
		return volumeGid
	}
	return ""
}


func (dswp *desiredStateOfWorldPopulator) createVolumeSpec(
	podVolume v1.Volume, podName string, podNamespace string, mountsMap map[string]bool, devicesMap map[string]bool) (*v1.PersistentVolumeClaim, *volume.Spec, string, error) {
	if pvcSource := podVolume.VolumeSource.PersistentVolumeClaim; pvcSource != nil {
		klog.Infof("Found PVC, ClaimName: %q/%q", podNamespace, pvcSource.ClaimName)

		// If podVolume is a PVC, fetch the real PV behind the claim
		pvc, err := dswp.getPVCExtractPV(podNamespace, pvcSource.ClaimName)
		if err != nil {
			return nil, nil, "", fmt.Errorf(
				"error processing PVC %q/%q: %v",
				podNamespace,
				pvcSource.ClaimName,
				err)
		}
		pvName, pvcUID := pvc.Spec.VolumeName, pvc.UID

		klog.Infof(
			"Found bound PV for PVC (ClaimName %q/%q pvcUID %v): pvName=%q",
			podNamespace,
			pvcSource.ClaimName,
			pvcUID,
			pvName)

		volumeSpec, volumeGidValue, err := dswp.getPVSpec(pvName, pvcSource.ReadOnly, pvcUID)
		if err != nil {
			return nil, nil, "", fmt.Errorf(
				"error processing PVC %q/%q: %v",
				podNamespace,
				pvcSource.ClaimName,
				err)
		}

		klog.Infof(
			"Extracted volumeSpec (%v) from bound PV (pvName %q) and PVC (ClaimName %q/%q pvcUID %v)",
			volumeSpec.Name(),
			pvName,
			podNamespace,
			pvcSource.ClaimName,
			pvcUID)


		// TODO: remove feature gate check after no longer needed
		if utilfeature.DefaultFeatureGate.Enabled(features.BlockVolume) {
			volumeMode, err := util.GetVolumeMode(volumeSpec)
			if err != nil {
				return nil, nil, "", err
			}
			// Error if a container has volumeMounts but the volumeMode of PVC isn't Filesystem
			if mountsMap[podVolume.Name] && volumeMode != v1.PersistentVolumeFilesystem {
				return nil, nil, "", fmt.Errorf(
					"Volume %q has volumeMode %q, but is specified in volumeMounts for pod %q/%q",
					podVolume.Name,
					volumeMode,
					podNamespace,
					podName)
			}
			// Error if a container has volumeDevices but the volumeMode of PVC isn't Block
			if devicesMap[podVolume.Name] && volumeMode != v1.PersistentVolumeBlock {
				return nil, nil, "", fmt.Errorf(
					"Volume %q has volumeMode %q, but is specified in volumeDevices for pod %q/%q",
					podVolume.Name,
					volumeMode,
					podNamespace,
					podName)
			}
		}
		return pvc, volumeSpec, volumeGidValue, nil
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
		klog.Infof("desiredStateOfWorldPopulator processPodVolumes allVolumesAdd pod: %v/%v", pod.Namespace, pod.Name)
		dswp.markPodProcessed(uniquePodName)
		// New pod has been synced. Re-mount all volumes that need it (e.g. DownwardAPI)
		dswp.actualStateOfWorld.MarkRemountRequired(uniquePodName)
	}

}





































