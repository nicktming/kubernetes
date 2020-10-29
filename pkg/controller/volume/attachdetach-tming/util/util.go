package util

import (
	"k8s.io/kubernetes/pkg/controller/volume/attachdetach-tming/cache"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/volume/util"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/klog"
	"fmt"
)

func CreateVolumeSpec(podVolume v1.Volume, podNamespace string, pvcLister corelisters.PersistentVolumeClaimLister, pvLister corelisters.PersistentVolumeLister) (*volume.Spec, error) {
	if pvcSource := podVolume.VolumeSource.PersistentVolumeClaim; pvcSource != nil {
		klog.V(10).Infof(
			"Found PVC, ClaimName: %q/%q",
			podNamespace,
			pvcSource.ClaimName)

		pvName, pvcUID, err := getPVCFromCacheExtractPV(podNamespace, pvcSource.ClaimName, pvcLister)
		if err != nil {
			return nil, fmt.Errorf(
				"error processing PVC %q/%q: %v",
				podNamespace,
				pvcSource.ClaimName,
				err)
		}
		klog.Infof(
			"Found bound PV for PVC (ClaimName %q/%q pvcUID %v): pvName=%q",
			podNamespace,
			pvcSource.ClaimName,
			pvcUID,
			pvName)

		// Fetch actual PV object
		volumeSpec, err := getPVSpecFromCache(
			pvName, pvcSource.ReadOnly, pvcUID, pvLister)
		if err != nil {
			return nil, fmt.Errorf(
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

		return volumeSpec, nil
	}

	clonedPodVolume := podVolume.DeepCopy()

	return volume.NewSpecFromVolume(clonedPodVolume), nil
}

func getPVSpecFromCache(name string, pvcReadOnly bool, expectedClaimUID types.UID, pvLister corelisters.PersistentVolumeLister) (*volume.Spec, error) {
	pv, err := pvLister.Get(name)
	if err != nil {
		return nil, fmt.Errorf("failed to find PV %q in PVInformer cache: %v", name, err)
	}

	if pv.Spec.ClaimRef == nil {
		return nil, fmt.Errorf(
			"found PV object %q but it has a nil pv.Spec.ClaimRef indicating it is not yet bound to the claim",
			name)
	}

	if pv.Spec.ClaimRef.UID != expectedClaimUID {
		return nil, fmt.Errorf(
			"found PV object %q but its pv.Spec.ClaimRef.UID (%q) does not point to claim.UID (%q)",
			name,
			pv.Spec.ClaimRef.UID,
			expectedClaimUID)
	}

	// Do not return the object from the informer, since the store is shared it
	// may be mutated by another consumer.
	clonedPV := pv.DeepCopy()

	return volume.NewSpecFromPersistentVolume(clonedPV, pvcReadOnly), nil
}

func getPVCFromCacheExtractPV(namespace string, name string, pvcLister corelisters.PersistentVolumeClaimLister) (string, types.UID, error) {
	pvc, err := pvcLister.PersistentVolumeClaims(namespace).Get(name)
	if err != nil {
		return "", "", fmt.Errorf("failed to find PVC %s/%s in PVCInformer cache: %v", namespace, name, err)
	}

	if pvc.Status.Phase != v1.ClaimBound || pvc.Spec.VolumeName == "" {
		return "", "", fmt.Errorf(
			"PVC %s/%s has non-bound phase (%q) or empty pvc.Spec.VolumeName (%q)",
			namespace,
			name,
			pvc.Status.Phase,
			pvc.Spec.VolumeName)
	}

	return pvc.Spec.VolumeName, pvc.UID, nil
}

func DetermineVolumeAction(pod *v1.Pod, desiredStateOfWorld cache.DesiredStateOfWorld, defaultAction bool) bool {
	if pod == nil || len(pod.Spec.Volumes) <= 0 {
		return defaultAction
	}

	nodeName := types.NodeName(pod.Spec.NodeName)
	keepTerminatedPodVolume := desiredStateOfWorld.GetKeepTerminatedPodVolumesForNode(nodeName)

	if util.IsPodTerminated(pod, pod.Status) {
		return keepTerminatedPodVolume
	}

	return defaultAction
}

// 1. pod所调度的node必须在dsw纳管里, 不存在则返回
// 2. 遍历pod所有的volumes
// 3. 针对每一个volume, 如果不是attachable? 跳过


// ProcessPodVolumes processes the volumes in the given pod and adds them to the
// desired state of the world if addVolumes is true, otherwise it removes them.
func ProcessPodVolumes(pod *v1.Pod, addVolumes bool, desiredStateOfWorld cache.DesiredStateOfWorld, volumePluginMgr *volume.VolumePluginMgr, pvcLister corelisters.PersistentVolumeClaimLister, pvLister corelisters.PersistentVolumeLister) {
	if pod == nil {
		return
	}
	if len(pod.Spec.Volumes) <= 0 {
		klog.V(10).Infof("Skipping processing of pod %q/%q: it has no volumes.",
			pod.Namespace,
			pod.Name)
		return
	}

	nodeName := types.NodeName(pod.Spec.NodeName)
	if nodeName == "" {
		klog.V(10).Infof(
			"Skipping processing of pod %q/%q: it is not scheduled to a node.",
			pod.Namespace,
			pod.Name)
		return
	} else if !desiredStateOfWorld.NodeExists(nodeName) {
		// If the node the pod is scheduled to does not exist in the desired
		// state of the world data structure, that indicates the node is not
		// yet managed by the controller. Therefore, ignore the pod.
		klog.V(4).Infof(
			"Skipping processing of pod %q/%q: it is scheduled to node %q which is not managed by the controller.",
			pod.Namespace,
			pod.Name,
			nodeName)
		return
	}

	for _, podVolume := range pod.Spec.Volumes {
		volumeSpec, err := CreateVolumeSpec(podVolume, pod.Namespace, pvcLister, pvLister)
		if err != nil {
			klog.V(10).Infof(
				"Error processing volume %q for pod %q/%q: %v",
				podVolume.Name,
				pod.Namespace,
				pod.Name,
				err)
			continue
		}

		attachableVolumePlugin, err :=
			volumePluginMgr.FindAttachablePluginBySpec(volumeSpec)
		if err != nil || attachableVolumePlugin == nil {
			klog.V(10).Infof(
				"Skipping volume %q for pod %q/%q: it does not implement attacher interface. err=%v",
				podVolume.Name,
				pod.Namespace,
				pod.Name,
				err)
			continue
		}

		uniquePodName := util.GetUniquePodName(pod)

	}

}

