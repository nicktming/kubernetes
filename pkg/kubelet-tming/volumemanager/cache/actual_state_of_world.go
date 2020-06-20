package cache

import (
	"sync"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
	volumetypes "k8s.io/kubernetes/pkg/volume/util/types"
)


type ActualStateOfWorld interface {
	// MarkRemountRequired marks each volume that is successfully attached and
	// mounted for the specified pod as requiring remount (if the plugin for the
	// volume indicates it requires remounting on pod updates). Atomically
	// updating volumes depend on this to update the contents of the volume on
	// pod update.
	MarkRemountRequired(podName volumetypes.UniquePodName)
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