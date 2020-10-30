package cache

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/klog"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/volume"
	//"k8s.io/kubernetes/pkg/volume/util"
	"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
)

type ActualStateOfWorld interface {

	operationexecutor.ActualStateOfWorldAttacherUpdater

	IsVolumeAttachedToNode(volumeName v1.UniqueVolumeName, nodeName types.NodeName) bool

	// ResetDetachRequestTime resets the detachRequestTime to 0 which indicates there is no detach
	// request any more for the volume
	ResetDetachRequestTime(volumeName v1.UniqueVolumeName, nodeName types.NodeName)
}

// AttachedVolume represents a volume that is attached to a node.
type AttachedVolume struct {
	operationexecutor.AttachedVolume

	// MountedByNode indicates that this volume has been mounted by the node and
	// is unsafe to detach.
	// The value is set and unset by SetVolumeMountedByNode(...).
	MountedByNode bool

	// DetachRequestedTime is used to capture the desire to detach this volume.
	// When the volume is newly created this value is set to time zero.
	// It is set to current time, when SetDetachRequestTime(...) is called, if it
	// was previously set to zero (other wise its value remains the same).
	// It is reset to zero on ResetDetachRequestTime(...) calls.
	DetachRequestedTime time.Time
}

// NewActualStateOfWorld returns a new instance of ActualStateOfWorld.
func NewActualStateOfWorld(volumePluginMgr *volume.VolumePluginMgr) ActualStateOfWorld {
	return &actualStateOfWorld{
		attachedVolumes:        make(map[v1.UniqueVolumeName]attachedVolume),
		nodesToUpdateStatusFor: make(map[types.NodeName]nodeToUpdateStatusFor),
		volumePluginMgr:        volumePluginMgr,
	}
}

type actualStateOfWorld struct {
	// attachedVolumes is a map containing the set of volumes the attach/detach
	// controller believes to be successfully attached to the nodes it is
	// managing. The key in this map is the name of the volume and the value is
	// an object containing more information about the attached volume.
	attachedVolumes map[v1.UniqueVolumeName]attachedVolume

	// nodesToUpdateStatusFor is a map containing the set of nodes for which to
	// update the VolumesAttached Status field. The key in this map is the name
	// of the node and the value is an object containing more information about
	// the node (including the list of volumes to report attached).
	nodesToUpdateStatusFor map[types.NodeName]nodeToUpdateStatusFor

	// volumePluginMgr is the volume plugin manager used to create volume
	// plugin objects.
	volumePluginMgr *volume.VolumePluginMgr

	sync.RWMutex
}

// The volume object represents a volume the attach/detach controller
// believes to be successfully attached to a node it is managing.
type attachedVolume struct {
	// volumeName contains the unique identifier for this volume.
	volumeName v1.UniqueVolumeName

	// spec is the volume spec containing the specification for this volume.
	// Used to generate the volume plugin object, and passed to attach/detach
	// methods.
	spec *volume.Spec

	// nodesAttachedTo is a map containing the set of nodes this volume has
	// been attached to. The key in this map is the name of the
	// node and the value is a node object containing more information about
	// the node.
	nodesAttachedTo map[types.NodeName]nodeAttachedTo

	// devicePath contains the path on the node where the volume is attached
	devicePath string
}

// The nodeAttachedTo object represents a node that has volumes attached to it
// or trying to attach to it.
type nodeAttachedTo struct {
	// nodeName contains the name of this node.
	nodeName types.NodeName

	// mountedByNode indicates that this node/volume combo is mounted by the
	// node and is unsafe to detach
	mountedByNode bool

	// attachConfirmed indicates that the storage system verified the volume has been attached to this node.
	// This value is set to false when an attach  operation fails and the volume may be attached or not.
	attachedConfirmed bool

	// detachRequestedTime used to capture the desire to detach this volume
	detachRequestedTime time.Time
}

// nodeToUpdateStatusFor is an object that reflects a node that has one or more
// volume attached. It keeps track of the volumes that should be reported as
// attached in the Node's Status API object.
type nodeToUpdateStatusFor struct {
	// nodeName contains the name of this node.
	nodeName types.NodeName

	// statusUpdateNeeded indicates that the value of the VolumesAttached field
	// in the Node's Status API object should be updated. This should be set to
	// true whenever a volume is added or deleted from
	// volumesToReportAsAttached. It should be reset whenever the status is
	// updated.
	statusUpdateNeeded bool

	// volumesToReportAsAttached is the list of volumes that should be reported
	// as attached in the Node's status (note that this may differ from the
	// actual list of attached volumes since volumes should be removed from this
	// list as soon a detach operation is considered, before the detach
	// operation is triggered).
	volumesToReportAsAttached map[v1.UniqueVolumeName]v1.UniqueVolumeName
}

func (asw *actualStateOfWorld) IsVolumeAttachedToNode(
	volumeName v1.UniqueVolumeName, nodeName types.NodeName) bool {
	asw.RLock()
	defer asw.RUnlock()

	volumeObj, volumeExists := asw.attachedVolumes[volumeName]
	if volumeExists {
		if node, nodeExists := volumeObj.nodesAttachedTo[nodeName]; nodeExists {
			return node.attachedConfirmed
		}
	}

	return false
}

func (asw *actualStateOfWorld) getNodeAndVolume(
	volumeName v1.UniqueVolumeName, nodeName types.NodeName) (attachedVolume, nodeAttachedTo, error) {

	volumeObj, volumeExists := asw.attachedVolumes[volumeName]
	if volumeExists {
		nodeObj, nodeExists := volumeObj.nodesAttachedTo[nodeName]
		if nodeExists {
			return volumeObj, nodeObj, nil
		}
	}

	return attachedVolume{}, nodeAttachedTo{}, fmt.Errorf("volume %v is no longer attached to the node %q",
		volumeName,
		nodeName)
}

func (asw *actualStateOfWorld) ResetDetachRequestTime(
	volumeName v1.UniqueVolumeName, nodeName types.NodeName) {
	asw.Lock()
	defer asw.Unlock()

	volumeObj, nodeObj, err := asw.getNodeAndVolume(volumeName, nodeName)
	if err != nil {
		klog.Errorf("Failed to ResetDetachRequestTime with error: %v", err)
		return
	}
	nodeObj.detachRequestedTime = time.Time{}
	volumeObj.nodesAttachedTo[nodeName] = nodeObj
}


func (asw *actualStateOfWorld) MarkVolumeAsAttached(
uniqueName v1.UniqueVolumeName, volumeSpec *volume.Spec, nodeName types.NodeName, devicePath string) error {
	//_, err := asw.AddVolumeNode(uniqueName, volumeSpec, nodeName, devicePath, true)

	klog.Infof("=====>volumename:%v nodeName: %v devicePath: %v mark as attached", uniqueName, nodeName, devicePath)
	return nil
}

func (asw *actualStateOfWorld) MarkVolumeAsDetached(
volumeName v1.UniqueVolumeName, nodeName types.NodeName) {
	//asw.DeleteVolumeNode(volumeName, nodeName)

	klog.Infof("=====>volumename:%v nodeName: %v mark as detached", volumeName, nodeName)
}

func (asw *actualStateOfWorld) MarkVolumeAsUncertain(
uniqueName v1.UniqueVolumeName, volumeSpec *volume.Spec, nodeName types.NodeName) error {

	//_, err := asw.AddVolumeNode(uniqueName, volumeSpec, nodeName, "", false /* isAttached */)

	klog.Infof("=====>volumename:%v nodeName: %v mark as uncertain", uniqueName, nodeName)
	return nil
}

func (asw *actualStateOfWorld) RemoveVolumeFromReportAsAttached(
volumeName v1.UniqueVolumeName, nodeName types.NodeName) error {
	asw.Lock()
	defer asw.Unlock()
	//return asw.removeVolumeFromReportAsAttached(volumeName, nodeName)

	klog.Infof("=====>volumename:%v nodeName: %v mark as uncertain", volumeName, nodeName)
	return nil
}

func (asw *actualStateOfWorld) AddVolumeToReportAsAttached(
volumeName v1.UniqueVolumeName, nodeName types.NodeName) {
	asw.Lock()
	defer asw.Unlock()
	//asw.addVolumeToReportAsAttached(volumeName, nodeName)
	klog.Infof("=====>volumename:%v nodeName: %v mark as uncertain", volumeName, nodeName)
}


















































































































