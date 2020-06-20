package cache

import (
	"sync"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
	volumetypes "k8s.io/kubernetes/pkg/volume/util/types"
	"fmt"
)


type ActualStateOfWorld interface {
	// MarkRemountRequired marks each volume that is successfully attached and
	// mounted for the specified pod as requiring remount (if the plugin for the
	// volume indicates it requires remounting on pod updates). Atomically
	// updating volumes depend on this to update the contents of the volume on
	// pod update.
	MarkRemountRequired(podName volumetypes.UniquePodName)

	// PodExistsInVolume returns true if the given pod exists in the list of
	// mountedPods for the given volume in the cache, indicating that the volume
	// is attached to this node and the pod has successfully mounted it.
	// If a pod with the same unique name does not exist under the specified
	// volume, false is returned.
	// If a volume with the name volumeName does not exist in the list of
	// attached volumes, a volumeNotAttachedError is returned indicating the
	// given volume is not yet attached.
	// If the given volumeName/podName combo exists but the value of
	// remountRequired is true, a remountRequiredError is returned indicating
	// the given volume has been successfully mounted to this pod but should be
	// remounted to reflect changes in the referencing pod. Atomically updating
	// volumes, depend on this to update the contents of the volume.
	// All volume mounting calls should be idempotent so a second mount call for
	// volumes that do not need to update contents should not fail.
	PodExistsInVolume(podName volumetypes.UniquePodName, volumeName v1.UniqueVolumeName) (bool, string, error)

}


// IsVolumeNotAttachedError returns true if the specified error is a
// volumeNotAttachedError.
func IsVolumeNotAttachedError(err error) bool {
	_, ok := err.(volumeNotAttachedError)
	return ok
}

// IsRemountRequiredError returns true if the specified error is a
// remountRequiredError.
func IsRemountRequiredError(err error) bool {
	_, ok := err.(remountRequiredError)
	return ok
}


// MountedVolume represents a volume that has successfully been mounted to a pod.
type MountedVolume struct {
	operationexecutor.MountedVolume
}

// AttachedVolume represents a volume that is attached to a node.
type AttachedVolume struct {
	operationexecutor.AttachedVolume

	// GloballyMounted indicates that the volume is mounted to the underlying
	// device at a global mount point. This global mount point must unmounted
	// prior to detach.
	GloballyMounted bool
}

// NewActualStateOfWorld returns a new instance of ActualStateOfWorld.
func NewActualStateOfWorld(
nodeName types.NodeName,
volumePluginMgr *volume.VolumePluginMgr) ActualStateOfWorld {
	return &actualStateOfWorld{
		nodeName:        nodeName,
		attachedVolumes: make(map[v1.UniqueVolumeName]attachedVolume),
		volumePluginMgr: volumePluginMgr,
	}
}

type actualStateOfWorld struct {
	// nodeName is the name of this node. This value is passed to Attach/Detach
	nodeName 		types.NodeName
	attachedVolumes 	map[v1.UniqueVolumeName]attachedVolume

	volumePluginMgr 	*volume.VolumePluginMgr
	sync.RWMutex
}

// attachedVolume represents a volume the kubelet volume manager believes to be
// successfully attached to a node it is managing. Volume types that do not
// implement an attacher are assumed to be in this state.
type attachedVolume struct {
	// volumeName contains the unique identifier for this volume.
	volumeName v1.UniqueVolumeName

	// mountedPods is a map containing the set of pods that this volume has been
	// successfully mounted to. The key in this map is the name of the pod and
	// the value is a mountedPod object containing more information about the
	// pod.
	mountedPods map[volumetypes.UniquePodName]mountedPod

	// spec is the volume spec containing the specification for this volume.
	// Used to generate the volume plugin object, and passed to plugin methods.
	// In particular, the Unmount method uses spec.Name() as the volumeSpecName
	// in the mount path:
	// /var/lib/kubelet/pods/{podUID}/volumes/{escapeQualifiedPluginName}/{volumeSpecName}/
	spec *volume.Spec

	// pluginName is the Unescaped Qualified name of the volume plugin used to
	// attach and mount this volume. It is stored separately in case the full
	// volume spec (everything except the name) can not be reconstructed for a
	// volume that should be unmounted (which would be the case for a mount path
	// read from disk without a full volume spec).
	pluginName string

	// pluginIsAttachable indicates the volume plugin used to attach and mount
	// this volume implements the volume.Attacher interface
	pluginIsAttachable bool

	// globallyMounted indicates that the volume is mounted to the underlying
	// device at a global mount point. This global mount point must be unmounted
	// prior to detach.
	globallyMounted bool

	// devicePath contains the path on the node where the volume is attached for
	// attachable volumes
	devicePath string

	// deviceMountPath contains the path on the node where the device should
	// be mounted after it is attached.
	deviceMountPath string
}


// The mountedPod object represents a pod for which the kubelet volume manager
// believes the underlying volume has been successfully been mounted.
type mountedPod struct {
	// the name of the pod
	podName volumetypes.UniquePodName

	// the UID of the pod
	podUID types.UID

	// mounter used to mount
	mounter volume.Mounter

	// mapper used to block volumes support
	blockVolumeMapper volume.BlockVolumeMapper

	// spec is the volume spec containing the specification for this volume.
	// Used to generate the volume plugin object, and passed to plugin methods.
	// In particular, the Unmount method uses spec.Name() as the volumeSpecName
	// in the mount path:
	// /var/lib/kubelet/pods/{podUID}/volumes/{escapeQualifiedPluginName}/{volumeSpecName}/
	volumeSpec *volume.Spec

	// outerVolumeSpecName is the volume.Spec.Name() of the volume as referenced
	// directly in the pod. If the volume was referenced through a persistent
	// volume claim, this contains the volume.Spec.Name() of the persistent
	// volume claim
	outerVolumeSpecName string

	// remountRequired indicates the underlying volume has been successfully
	// mounted to this pod but it should be remounted to reflect changes in the
	// referencing pod.
	// Atomically updating volumes depend on this to update the contents of the
	// volume. All volume mounting calls should be idempotent so a second mount
	// call for volumes that do not need to update contents should not fail.
	remountRequired bool

	// volumeGidValue contains the value of the GID annotation, if present.
	volumeGidValue string

	// fsResizeRequired indicates the underlying volume has been successfully
	// mounted to this pod but its size has been expanded after that.
	fsResizeRequired bool
}

// volumeNotAttachedError is an error returned when PodExistsInVolume() fails to
// find specified volume in the list of attached volumes.
type volumeNotAttachedError struct {
	volumeName v1.UniqueVolumeName
}

func (err volumeNotAttachedError) Error() string {
	return fmt.Sprintf(
		"volumeName %q does not exist in the list of attached volumes",
		err.volumeName)
}

func newVolumeNotAttachedError(volumeName v1.UniqueVolumeName) error {
	return volumeNotAttachedError{
		volumeName: volumeName,
	}
}

// remountRequiredError is an error returned when PodExistsInVolume() found
// volume/pod attached/mounted but remountRequired was true, indicating the
// given volume should be remounted to the pod to reflect changes in the
// referencing pod.
type remountRequiredError struct {
	volumeName v1.UniqueVolumeName
	podName    volumetypes.UniquePodName
}

func (err remountRequiredError) Error() string {
	return fmt.Sprintf(
		"volumeName %q is mounted to %q but should be remounted",
		err.volumeName, err.podName)
}

func newRemountRequiredError(
volumeName v1.UniqueVolumeName, podName volumetypes.UniquePodName) error {
	return remountRequiredError{
		volumeName: volumeName,
		podName:    podName,
	}
}

// fsResizeRequiredError is an error returned when PodExistsInVolume() found
// volume/pod attached/mounted but fsResizeRequired was true, indicating the
// given volume receives an resize request after attached/mounted.
type fsResizeRequiredError struct {
	volumeName v1.UniqueVolumeName
	podName    volumetypes.UniquePodName
}

func (err fsResizeRequiredError) Error() string {
	return fmt.Sprintf(
		"volumeName %q mounted to %q needs to resize file system",
		err.volumeName, err.podName)
}

func newFsResizeRequiredError(
volumeName v1.UniqueVolumeName, podName volumetypes.UniquePodName) error {
	return fsResizeRequiredError{
		volumeName: volumeName,
		podName:    podName,
	}
}

// IsFSResizeRequiredError returns true if the specified error is a
// fsResizeRequiredError.
func IsFSResizeRequiredError(err error) bool {
	_, ok := err.(fsResizeRequiredError)
	return ok
}


func (asw *actualStateOfWorld) PodExistsInVolume(
	podName 		volumetypes.UniquePodName,
	volumeName 		v1.UniqueVolumeName) (bool, string, error) {

	asw.RLock()
	defer asw.RUnlock()

	volumeObj, volumeExists := asw.attachedVolumes[volumeName]
	if !volumeExists {
		return false, "", newVolumeNotAttachedError(volumeName)
	}

	podObj, podExists := volumeObj.mountedPods[podName]
	if podExists {
		if podObj.remountRequired {
			return true, volumeObj.devicePath, newRemountRequiredError(volumeObj.volumeName, podObj.podName)
		}

		if podObj.fsResizeRequired &&
			utilfeature.DefaultFeatureGate.Enabled(features.ExpandInUsePersistentVolumes) {
			return true, volumeObj.devicePath, newFsResizeRequiredError(volumeObj.volumeName, podObj.podName)
		}
	}

	return podExists, volumeObj.devicePath, nil
}

func (asw *actualStateOfWorld) MarkRemountRequired(
	podName volumetypes.UniquePodName) {
	asw.Lock()
	defer asw.Unlock()
	for volumeName, volumeObj := range asw.attachedVolumes {
		for mountedPodName, podObj := range volumeObj.mountedPods {
			if mountedPodName != podName {
				continue
			}

			volumePlugin, err :=
				asw.volumePluginMgr.FindPluginBySpec(podObj.volumeSpec)
			if err != nil || volumePlugin == nil {
				// Log and continue processing
				klog.Errorf(
					"MarkRemountRequired failed to FindPluginBySpec for pod %q (podUid %q) volume: %q (volSpecName: %q)",
					podObj.podName,
					podObj.podUID,
					volumeObj.volumeName,
					podObj.volumeSpec.Name())
				continue
			}

			if volumePlugin.RequiresRemount() {
				podObj.remountRequired = true
				asw.attachedVolumes[volumeName].mountedPods[podName] = podObj
			}
		}
	}
}