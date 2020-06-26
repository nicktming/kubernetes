package cache

import (
	"fmt"
	"sync"

	"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/volume/util/types"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/kubernetes/pkg/volume/util"
	apiv1resource "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/klog"
	"encoding/json"
)

type DesiredStateOfWorld interface {
	// AddPodToVolume adds the given pod to the given volume in the cache
	// indicating the specified pod should mount the specified volume.
	// A unique volumeName is generated from the volumeSpec and returned on
	// success.
	// If no volume plugin can support the given volumeSpec or more than one
	// plugin can support it, an error is returned.
	// If a volume with the name volumeName does not exist in the list of
	// volumes that should be attached to this node, the volume is implicitly
	// added.
	// If a pod with the same unique name already exists under the specified
	// volume, this is a no-op.
	AddPodToVolume(podName types.UniquePodName, pod *v1.Pod, volumeSpec *volume.Spec, outerVolumeSpecName string, volumeGidValue string) (v1.UniqueVolumeName, error)


	// GetVolumesToMount generates and returns a list of volumes that should be
	// attached to this node and the pods they should be mounted to based on the
	// current desired state of the world.
	GetVolumesToMount() []VolumeToMount

	// MarkVolumesReportedInUse sets the ReportedInUse value to true for the
	// reportedVolumes. For volumes not in the reportedVolumes list, the
	// ReportedInUse value is reset to false. The default ReportedInUse value
	// for a newly created volume is false.
	// When set to true this value indicates that the volume was successfully
	// added to the VolumesInUse field in the node's status. Mount operation needs
	// to check this value before issuing the operation.
	// If a volume in the reportedVolumes list does not exist in the list of
	// volumes that should be attached to this node, it is skipped without error.
	MarkVolumesReportedInUse(reportedVolumes []v1.UniqueVolumeName)
}

type VolumeToMount struct {
	operationexecutor.VolumeToMount
}

func NewDesiredStateOfWorld(volumePluginMgr *volume.VolumePluginMgr) DesiredStateOfWorld {
	return &desiredStateOfWorld {
		volumesToMount:		make(map[v1.UniqueVolumeName]volumeToMount),
		volumePluginMgr: 	volumePluginMgr,
	}
}

type desiredStateOfWorld struct {
	volumesToMount 		map[v1.UniqueVolumeName]volumeToMount
	volumePluginMgr 	*volume.VolumePluginMgr
	sync.RWMutex
}


// The volume object represents a volume that should be attached to this node,
// and mounted to podsToMount.
type volumeToMount struct {
	// volumeName contains the unique identifier for this volume.
	volumeName v1.UniqueVolumeName

	// podsToMount is a map containing the set of pods that reference this
	// volume and should mount it once it is attached. The key in the map is
	// the name of the pod and the value is a pod object containing more
	// information about the pod.
	podsToMount map[types.UniquePodName]podToMount

	// pluginIsAttachable indicates that the plugin for this volume implements
	// the volume.Attacher interface
	pluginIsAttachable bool

	// pluginIsDeviceMountable indicates that the plugin for this volume implements
	// the volume.DeviceMounter interface
	pluginIsDeviceMountable bool

	// volumeGidValue contains the value of the GID annotation, if present.
	volumeGidValue string

	// reportedInUse indicates that the volume was successfully added to the
	// VolumesInUse field in the node's status.
	reportedInUse bool

	// desiredSizeLimit indicates the desired upper bound on the size of the volume
	// (if so implemented)
	desiredSizeLimit *resource.Quantity
}


// The pod object represents a pod that references the underlying volume and
// should mount it once it is attached.
type podToMount struct {
	// podName contains the name of this pod.
	podName types.UniquePodName

	// Pod to mount the volume to. Used to create NewMounter.
	pod *v1.Pod

	// volume spec containing the specification for this volume. Used to
	// generate the volume plugin object, and passed to plugin methods.
	// For non-PVC volumes this is the same as defined in the pod object. For
	// PVC volumes it is from the dereferenced PV object.
	volumeSpec *volume.Spec

	// outerVolumeSpecName is the volume.Spec.Name() of the volume as referenced
	// directly in the pod. If the volume was referenced through a persistent
	// volume claim, this contains the volume.Spec.Name() of the persistent
	// volume claim
	outerVolumeSpecName string
}


func (dsw *desiredStateOfWorld) AddPodToVolume(
	podName types.UniquePodName,
	pod *v1.Pod,
	volumeSpec *volume.Spec,
	outerVolumeSpecName string,
	volumeGidValue string) (v1.UniqueVolumeName, error) {
	dsw.Lock()
	defer dsw.Unlock()

	volumePlugin, err := dsw.volumePluginMgr.FindPluginBySpec(volumeSpec)
	if err != nil || volumePlugin == nil {
		return "", fmt.Errorf(
			"failed to get Plugin from volumeSpec for volume %q err=%v",
			volumeSpec.Name(),
			err)
	}
	pretty_volumeSpec, _ := json.MarshalIndent(volumeSpec, "", "\t")

	klog.Infof("desiredStateOfWorld found volumeplugin : %v, volumeSpec: %v", volumePlugin.GetPluginName(), string(pretty_volumeSpec))

	var volumeName v1.UniqueVolumeName

	// The unique volume name used depends on whether the volume is attachable/device-mountable
	// or not.
	attachable := dsw.isAttachableVolume(volumeSpec)
	deviceMountable := dsw.isDeviceMountableVolume(volumeSpec)


	if attachable || deviceMountable {
		// For attachable/device-mountable volumes, use the unique volume name as reported by
		// the plugin.
		volumeName, err =
			util.GetUniqueVolumeNameFromSpec(volumePlugin, volumeSpec)
		if err != nil {
			return "", fmt.Errorf(
				"failed to GetUniqueVolumeNameFromSpec for volumeSpec %q using volume plugin %q err=%v",
				volumeSpec.Name(),
				volumePlugin.GetPluginName(),
				err)
		}
	} else {
		// For non-attachable and non-device-mountable volumes, generate a unique name based on the pod
		// namespace and name and the name of the volume within the pod.
		volumeName = util.GetUniqueVolumeNameFromSpecWithPod(podName, volumePlugin, volumeSpec)
	}

	klog.Infof("desiredStateOfWorld  attachable : %v, deviceMountable: %v, volumeName: %v", attachable, deviceMountable, volumeName)

	if _, volumeExists := dsw.volumesToMount[volumeName]; !volumeExists {
		var sizeLimit *resource.Quantity
		if volumeSpec.Volume != nil {
			if util.IsLocalEphemeralVolume(*volumeSpec.Volume) {
				_, podLimits := apiv1resource.PodRequestsAndLimits(pod)
				ephemeralStorageLimit := podLimits[v1.ResourceEphemeralStorage]
				sizeLimit = resource.NewQuantity(ephemeralStorageLimit.Value(), resource.BinarySI)
				if volumeSpec.Volume.EmptyDir != nil &&
					volumeSpec.Volume.EmptyDir.SizeLimit != nil &&
					volumeSpec.Volume.EmptyDir.SizeLimit.Value() > 0 &&
					volumeSpec.Volume.EmptyDir.SizeLimit.Value() < sizeLimit.Value() {
					sizeLimit = resource.NewQuantity(volumeSpec.Volume.EmptyDir.SizeLimit.Value(), resource.BinarySI)
				}
			}
		}
		dsw.volumesToMount[volumeName] = volumeToMount{
			volumeName:              volumeName,
			podsToMount:             make(map[types.UniquePodName]podToMount),
			pluginIsAttachable:      attachable,
			pluginIsDeviceMountable: deviceMountable,
			volumeGidValue:          volumeGidValue,
			reportedInUse:           false,
			desiredSizeLimit:        sizeLimit,
		}
	}

	// Create new podToMount object. If it already exists, it is refreshed with
	// updated values (this is required for volumes that require remounting on
	// pod update, like Downward API volumes).
	dsw.volumesToMount[volumeName].podsToMount[podName] = podToMount{
		podName:             podName,
		pod:                 pod,
		volumeSpec:          volumeSpec,
		outerVolumeSpecName: outerVolumeSpecName,
	}
	return volumeName, nil
}


func (dsw *desiredStateOfWorld) GetVolumesToMount() []VolumeToMount {
	dsw.RLock()
	defer dsw.RUnlock()

	volumesToMount := make([]VolumeToMount, 0, len(dsw.volumesToMount))
	for volumeName, volumeObj := range dsw.volumesToMount {
		for podName, podObj := range volumeObj.podsToMount {
			volumesToMount = append(volumesToMount,
				VolumeToMount{
					VolumeToMount: operationexecutor.VolumeToMount{
						VolumeName: 			volumeName,
						PodName: 			podName,
						Pod:				podObj.pod,
						VolumeSpec:			podObj.volumeSpec,
						PluginIsAttachable:      	volumeObj.pluginIsAttachable,
						PluginIsDeviceMountable: 	volumeObj.pluginIsDeviceMountable,
						OuterVolumeSpecName:     	podObj.outerVolumeSpecName,
						VolumeGidValue:          	volumeObj.volumeGidValue,
						ReportedInUse:           	volumeObj.reportedInUse,
						DesiredSizeLimit:        	volumeObj.desiredSizeLimit,
					},
				})
		}
	}
	return volumesToMount
}

func (dsw *desiredStateOfWorld) isAttachableVolume(volumeSpec *volume.Spec) bool {
	attachableVolumePlugin, _ :=
		dsw.volumePluginMgr.FindAttachablePluginBySpec(volumeSpec)
	if attachableVolumePlugin != nil {
		volumeAttacher, err := attachableVolumePlugin.NewAttacher()
		if err == nil && volumeAttacher != nil {
			return true
		}
	}

	return false
}

func (dsw *desiredStateOfWorld) isDeviceMountableVolume(volumeSpec *volume.Spec) bool {
	deviceMountableVolumePlugin, _ := dsw.volumePluginMgr.FindDeviceMountablePluginBySpec(volumeSpec)
	if deviceMountableVolumePlugin != nil {
		volumeDeviceMounter, err := deviceMountableVolumePlugin.NewDeviceMounter()
		if err == nil && volumeDeviceMounter != nil {
			return true
		}
	}

	return false
}

func (dsw *desiredStateOfWorld) MarkVolumesReportedInUse(
		reportedVolumes		[]v1.UniqueVolumeName) {
	dsw.Lock()
	defer dsw.Unlock()

	reportedVolumesMap := make(map[v1.UniqueVolumeName]bool, len(reportedVolumes))

	for _, reportedVolume := range reportedVolumes {
		reportedVolumesMap[reportedVolume] = true
	}

	for volumeName, volumeObj := range dsw.volumesToMount {
		_, volumeReported := reportedVolumesMap[volumeName]
		volumeObj.reportedInUse = volumeReported
		dsw.volumesToMount[volumeName] = volumeObj
	}
}






















































