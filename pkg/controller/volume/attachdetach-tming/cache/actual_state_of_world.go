package cache

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/klog"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util"
	"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
)

type ActualStateOfWorld interface {

	operationexecutor.ActualStateOfWorldAttacherUpdater

	IsVolumeAttachedToNode(volumeName v1.UniqueVolumeName, nodeName types.NodeName) bool

	// ResetDetachRequestTime resets the detachRequestTime to 0 which indicates there is no detach
	// request any more for the volume
	ResetDetachRequestTime(volumeName v1.UniqueVolumeName, nodeName types.NodeName)

	SetNodeStatusUpdateNeeded(nodeName types.NodeName)

	GetVolumesToReportAttached() map[types.NodeName][]v1.AttachedVolume

	GetAttachedVolumes() []AttachedVolume

	SetDetachRequestTime(volumeName v1.UniqueVolumeName, nodeName types.NodeName) (time.Duration, error)


	SetVolumeMountedByNode(volumeName v1.UniqueVolumeName, nodeName types.NodeName, mounted bool) error

	GetAttachedVolumesForNode(nodeName types.NodeName) []AttachedVolume

	GetAttachedVolumesPerNode() map[types.NodeName][]operationexecutor.AttachedVolume

	GetNodesForAttachedVolume(volumeName v1.UniqueVolumeName) []types.NodeName
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
	_, err := asw.AddVolumeNode(uniqueName, volumeSpec, nodeName, devicePath, true)
	if err != nil {
		return err
	}

	klog.Infof("=====>volumename:%v nodeName: %v devicePath: %v mark as attached", uniqueName, nodeName, devicePath)
	return nil
}

func (asw *actualStateOfWorld) MarkVolumeAsDetached(
volumeName v1.UniqueVolumeName, nodeName types.NodeName) {
	asw.DeleteVolumeNode(volumeName, nodeName)

	klog.Infof("=====>volumename:%v nodeName: %v mark as detached", volumeName, nodeName)
}

func (asw *actualStateOfWorld) MarkVolumeAsUncertain(
uniqueName v1.UniqueVolumeName, volumeSpec *volume.Spec, nodeName types.NodeName) error {

	asw.AddVolumeNode(uniqueName, volumeSpec, nodeName, "", false /* isAttached */)

	klog.Infof("=====>volumename:%v nodeName: %v mark as uncertain", uniqueName, nodeName)
	return nil
}

func (asw *actualStateOfWorld) RemoveVolumeFromReportAsAttached(
volumeName v1.UniqueVolumeName, nodeName types.NodeName) error {
	asw.Lock()
	defer asw.Unlock()
	return asw.removeVolumeFromReportAsAttached(volumeName, nodeName)

	//klog.Infof("=====>volumename:%v nodeName: %v mark as uncertain", volumeName, nodeName)
	//return nil
}

func (asw *actualStateOfWorld) DeleteVolumeNode(
volumeName v1.UniqueVolumeName, nodeName types.NodeName) {
	asw.Lock()
	defer asw.Unlock()

	volumeObj, volumeExists := asw.attachedVolumes[volumeName]
	if !volumeExists {
		return
	}

	_, nodeExists := volumeObj.nodesAttachedTo[nodeName]
	if nodeExists {
		delete(asw.attachedVolumes[volumeName].nodesAttachedTo, nodeName)
	}

	if len(volumeObj.nodesAttachedTo) == 0 {
		delete(asw.attachedVolumes, volumeName)
	}

	// Remove volume from volumes to report as attached
	asw.removeVolumeFromReportAsAttached(volumeName, nodeName)
}

// Remove the volumeName from the node's volumesToReportAsAttached list
// This is an internal function and caller should acquire and release the lock
func (asw *actualStateOfWorld) removeVolumeFromReportAsAttached(
volumeName v1.UniqueVolumeName, nodeName types.NodeName) error {

	klog.Infof("=====>removeVolumeFromReportAsAttached volumename:%v nodeName: %v remove from report as attached", volumeName, nodeName)
	nodeToUpdate, nodeToUpdateExists := asw.nodesToUpdateStatusFor[nodeName]
	if nodeToUpdateExists {
		_, nodeToUpdateVolumeExists :=
			nodeToUpdate.volumesToReportAsAttached[volumeName]
		if nodeToUpdateVolumeExists {
			nodeToUpdate.statusUpdateNeeded = true
			delete(nodeToUpdate.volumesToReportAsAttached, volumeName)
			asw.nodesToUpdateStatusFor[nodeName] = nodeToUpdate
			return nil
		}
	}
	return fmt.Errorf("volume %q does not exist in volumesToReportAsAttached list or node %q does not exist in nodesToUpdateStatusFor list",
		volumeName,
		nodeName)

}

func (asw *actualStateOfWorld) AddVolumeToReportAsAttached(
volumeName v1.UniqueVolumeName, nodeName types.NodeName) {
	asw.Lock()
	defer asw.Unlock()
	asw.addVolumeToReportAsAttached(volumeName, nodeName)
	klog.Infof("=====>volumename:%v nodeName: %v mark as uncertain", volumeName, nodeName)
}

func (asw *actualStateOfWorld) AddVolumeNode(
	uniqueName v1.UniqueVolumeName, volumeSpec *volume.Spec,
	nodeName types.NodeName, devicePath string, isAttached bool) (v1.UniqueVolumeName, error) {

	asw.Lock()
	defer asw.Unlock()

	volumeName := uniqueName
	if volumeName == "" {
		if volumeSpec == nil {
			return volumeName, fmt.Errorf("volumeSpec cannot be nil if volumeName is empty")
		}
		attachableVolumePlugin, err := asw.volumePluginMgr.FindAttachablePluginBySpec(volumeSpec)
		if err != nil || attachableVolumePlugin == nil {
			return "", fmt.Errorf(
				"failed to get AttachablePlugin from volumeSpec for volume %q err=%v",
				volumeSpec.Name(),
				err)
		}

		volumeName, err = util.GetUniqueVolumeNameFromSpec(
			attachableVolumePlugin, volumeSpec)
		if err != nil {
			return "", fmt.Errorf(
				"failed to GetUniqueVolumeNameFromSpec for volumeSpec %q err=%v",
				volumeSpec.Name(),
				err)
		}
	}

	klog.Infof("actualStateOfWorld AddVolumeNode volumeName: %v, nodeName: %v", volumeName, nodeName)

	volumeObj, volumeExists := asw.attachedVolumes[volumeName]
	if !volumeExists {
		volumeObj = attachedVolume{
			volumeName:      volumeName,
			spec:            volumeSpec,
			nodesAttachedTo: make(map[types.NodeName]nodeAttachedTo),
			devicePath:      devicePath,
		}
	} else {
		// If volume object already exists, it indicates that the information would be out of date.
		// Update the fields for volume object except the nodes attached to the volumes.
		volumeObj.devicePath = devicePath
		volumeObj.spec = volumeSpec
		klog.Infof("Volume %q is already added to attachedVolume list to node %q, update device path %q",
			volumeName,
			nodeName,
			devicePath)
	}
	node, nodeExists := volumeObj.nodesAttachedTo[nodeName]
	if !nodeExists {
		// Create object if it doesn't exist.
		node = nodeAttachedTo{
			nodeName:            nodeName,
			mountedByNode:       true, // Assume mounted, until proven otherwise
			attachedConfirmed:   isAttached,
			detachRequestedTime: time.Time{},
		}
	} else {
		node.attachedConfirmed = isAttached
		klog.Infof("Volume %q is already added to attachedVolume list to the node %q, the current attach state is %t",
			volumeName,
			nodeName,
			isAttached)
	}

	volumeObj.nodesAttachedTo[nodeName] = node
	asw.attachedVolumes[volumeName] = volumeObj

	if isAttached {
		asw.addVolumeToReportAsAttached(volumeName, nodeName)
	}
	return volumeName, nil
}


// Add the volumeName to the node's volumesToReportAsAttached list
// This is an internal function and caller should acquire and release the lock
func (asw *actualStateOfWorld) addVolumeToReportAsAttached(
volumeName v1.UniqueVolumeName, nodeName types.NodeName) {
	// In case the volume/node entry is no longer in attachedVolume list, skip the rest
	if _, _, err := asw.getNodeAndVolume(volumeName, nodeName); err != nil {
		klog.V(4).Infof("Volume %q is no longer attached to node %q", volumeName, nodeName)
		return
	}
	nodeToUpdate, nodeToUpdateExists := asw.nodesToUpdateStatusFor[nodeName]
	if !nodeToUpdateExists {
		// Create object if it doesn't exist
		nodeToUpdate = nodeToUpdateStatusFor{
			nodeName:                  nodeName,
			statusUpdateNeeded:        true,
			volumesToReportAsAttached: make(map[v1.UniqueVolumeName]v1.UniqueVolumeName),
		}
		asw.nodesToUpdateStatusFor[nodeName] = nodeToUpdate
		klog.V(4).Infof("Add new node %q to nodesToUpdateStatusFor", nodeName)
	}
	_, nodeToUpdateVolumeExists :=
		nodeToUpdate.volumesToReportAsAttached[volumeName]
	if !nodeToUpdateVolumeExists {
		nodeToUpdate.statusUpdateNeeded = true
		nodeToUpdate.volumesToReportAsAttached[volumeName] = volumeName
		asw.nodesToUpdateStatusFor[nodeName] = nodeToUpdate
		klog.V(4).Infof("Report volume %q as attached to node %q", volumeName, nodeName)
	}
}

// Update the flag statusUpdateNeeded to indicate whether node status is already updated or
// needs to be updated again by the node status updater.
// If the specified node does not exist in the nodesToUpdateStatusFor list, log the error and return
// This is an internal function and caller should acquire and release the lock
func (asw *actualStateOfWorld) updateNodeStatusUpdateNeeded(nodeName types.NodeName, needed bool) error {
	nodeToUpdate, nodeToUpdateExists := asw.nodesToUpdateStatusFor[nodeName]
	if !nodeToUpdateExists {
		// should not happen
		errMsg := fmt.Sprintf("Failed to set statusUpdateNeeded to needed %t, because nodeName=%q does not exist",
			needed, nodeName)
		return fmt.Errorf(errMsg)
	}

	nodeToUpdate.statusUpdateNeeded = needed
	asw.nodesToUpdateStatusFor[nodeName] = nodeToUpdate

	return nil
}

func (asw *actualStateOfWorld) SetNodeStatusUpdateNeeded(nodeName types.NodeName) {
	asw.Lock()
	defer asw.Unlock()
	if err := asw.updateNodeStatusUpdateNeeded(nodeName, true); err != nil {
		klog.Warningf("Failed to update statusUpdateNeeded field in actual state of world: %v", err)
	}
}


func (asw *actualStateOfWorld) GetNodesForAttachedVolume(volumeName v1.UniqueVolumeName) []types.NodeName {
	asw.RLock()
	defer asw.RUnlock()

	volumeObj, volumeExists := asw.attachedVolumes[volumeName]
	if !volumeExists || len(volumeObj.nodesAttachedTo) == 0 {
		return []types.NodeName{}
	}

	nodes := []types.NodeName{}
	for k, nodesAttached := range volumeObj.nodesAttachedTo {
		if nodesAttached.attachedConfirmed {
			nodes = append(nodes, k)
		}
	}
	return nodes
}

func (asw *actualStateOfWorld) GetVolumesToReportAttached() map[types.NodeName][]v1.AttachedVolume {
	asw.RLock()
	defer asw.RUnlock()

	volumesToReportAttached := make(map[types.NodeName][]v1.AttachedVolume)
	for nodeName, nodeToUpdateObj := range asw.nodesToUpdateStatusFor {
		if nodeToUpdateObj.statusUpdateNeeded {
			attachedVolumes := make(
			[]v1.AttachedVolume,
				len(nodeToUpdateObj.volumesToReportAsAttached) /* len */)
			i := 0
			for _, volume := range nodeToUpdateObj.volumesToReportAsAttached {
				attachedVolumes[i] = v1.AttachedVolume{
					Name:       volume,
					DevicePath: asw.attachedVolumes[volume].devicePath,
				}
				i++
			}
			volumesToReportAttached[nodeToUpdateObj.nodeName] = attachedVolumes
		}
		// When GetVolumesToReportAttached is called by node status updater, the current status
		// of this node will be updated, so set the flag statusUpdateNeeded to false indicating
		// the current status is already updated.
		if err := asw.updateNodeStatusUpdateNeeded(nodeName, false); err != nil {
			klog.Errorf("Failed to update statusUpdateNeeded field when getting volumes: %v", err)
		}
	}

	return volumesToReportAttached
}


func (asw *actualStateOfWorld) GetAttachedVolumes() []AttachedVolume {
	asw.RLock()
	defer asw.RUnlock()

	attachedVolumes := make([]AttachedVolume, 0 /* len */, len(asw.attachedVolumes) /* cap */)
	for _, volumeObj := range asw.attachedVolumes {
		for _, nodeObj := range volumeObj.nodesAttachedTo {
			attachedVolumes = append(
				attachedVolumes,
				getAttachedVolume(&volumeObj, &nodeObj))
		}
	}

	return attachedVolumes
}

func (asw *actualStateOfWorld) GetAttachedVolumesForNode(
nodeName types.NodeName) []AttachedVolume {
	asw.RLock()
	defer asw.RUnlock()

	attachedVolumes := make(
	[]AttachedVolume, 0 /* len */, len(asw.attachedVolumes) /* cap */)
	for _, volumeObj := range asw.attachedVolumes {
		for actualNodeName, nodeObj := range volumeObj.nodesAttachedTo {
			if actualNodeName == nodeName {
				attachedVolumes = append(
					attachedVolumes,
					getAttachedVolume(&volumeObj, &nodeObj))
				break
			}
		}
	}

	return attachedVolumes
}

func (asw *actualStateOfWorld) GetAttachedVolumesPerNode() map[types.NodeName][]operationexecutor.AttachedVolume {
	asw.RLock()
	defer asw.RUnlock()

	attachedVolumesPerNode := make(map[types.NodeName][]operationexecutor.AttachedVolume)
	for _, volumeObj := range asw.attachedVolumes {
		for nodeName, nodeObj := range volumeObj.nodesAttachedTo {
			if nodeObj.attachedConfirmed {
				volumes := attachedVolumesPerNode[nodeName]
				volumes = append(volumes, getAttachedVolume(&volumeObj, &nodeObj).AttachedVolume)
				attachedVolumesPerNode[nodeName] = volumes
			}
		}
	}

	return attachedVolumesPerNode
}

func (asw *actualStateOfWorld) GetNodesToUpdateStatusFor() map[types.NodeName]nodeToUpdateStatusFor {
	return asw.nodesToUpdateStatusFor
}

func getAttachedVolume(
attachedVolume *attachedVolume,
nodeAttachedTo *nodeAttachedTo) AttachedVolume {
	return AttachedVolume{
		AttachedVolume: operationexecutor.AttachedVolume{
			VolumeName:         attachedVolume.volumeName,
			VolumeSpec:         attachedVolume.spec,
			NodeName:           nodeAttachedTo.nodeName,
			DevicePath:         attachedVolume.devicePath,
			PluginIsAttachable: true,
		},
		MountedByNode:       nodeAttachedTo.mountedByNode,
		DetachRequestedTime: nodeAttachedTo.detachRequestedTime}
}

func (asw *actualStateOfWorld) SetDetachRequestTime(
volumeName v1.UniqueVolumeName, nodeName types.NodeName) (time.Duration, error) {
	asw.Lock()
	defer asw.Unlock()

	volumeObj, nodeObj, err := asw.getNodeAndVolume(volumeName, nodeName)
	if err != nil {
		return 0, fmt.Errorf("Failed to set detach request time with error: %v", err)
	}
	// If there is no previous detach request, set it to the current time
	if nodeObj.detachRequestedTime.IsZero() {
		nodeObj.detachRequestedTime = time.Now()
		volumeObj.nodesAttachedTo[nodeName] = nodeObj
		klog.V(4).Infof("Set detach request time to current time for volume %v on node %q",
			volumeName,
			nodeName)
	}
	return time.Since(nodeObj.detachRequestedTime), nil
}

// 确认一个volume有没有被mount, 只是从volumeInUse中得到, 如果volumeInUse有, 则表示mount
// volumeInUse在volume_manager中获取, volume是desired volumes or attached volumes就表示in use
// volumeInUse有两种地方会更新:
// 1. kubelet的node condition方法中
// 2. attachdetachcontroller中node刚刚起来的时候

func (asw *actualStateOfWorld) SetVolumeMountedByNode(
volumeName v1.UniqueVolumeName, nodeName types.NodeName, mounted bool) error {
	asw.Lock()
	defer asw.Unlock()

	volumeObj, nodeObj, err := asw.getNodeAndVolume(volumeName, nodeName)
	if err != nil {
		return fmt.Errorf("Failed to SetVolumeMountedByNode with error: %v", err)
	}

	nodeObj.mountedByNode = mounted
	volumeObj.nodesAttachedTo[nodeName] = nodeObj
	klog.V(4).Infof("SetVolumeMountedByNode volume %v to the node %q mounted %t",
		volumeName,
		nodeName,
		mounted)
	return nil
}








































































































