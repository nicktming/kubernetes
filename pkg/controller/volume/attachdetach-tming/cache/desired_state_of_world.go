package cache

import (
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util/types"
	"sync"
	"fmt"
	"k8s.io/kubernetes/pkg/volume/util"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
)

type DesiredStateOfWorld interface {
	// GetKeepTerminatedPodVolumesForNode determines if node wants volumes to be
	// mounted and attached for terminated pods
	GetKeepTerminatedPodVolumesForNode(k8stypes.NodeName) bool

	NodeExists(nodeName k8stypes.NodeName) bool

	AddNode(nodeName k8stypes.NodeName, keepTerminatedPodVolumes bool)

	DeleteNode(nodeName k8stypes.NodeName) error

	DeletePod(podName types.UniquePodName, volumeName v1.UniqueVolumeName, nodeName k8stypes.NodeName)

	VolumeExists(volumeName v1.UniqueVolumeName, nodeName k8stypes.NodeName) bool

	AddPod(podName types.UniquePodName, pod *v1.Pod, volumeSpec *volume.Spec, nodeName k8stypes.NodeName) (v1.UniqueVolumeName, error)

	GetPodToAdd() map[types.UniquePodName]PodToAdd

	GetVolumesToAttach() []VolumeToAttach
}

type VolumeToAttach struct {
	operationexecutor.VolumeToAttach
}

type desiredStateOfWorld struct {

	nodesManaged 		map[k8stypes.NodeName]nodeManaged

	volumePluginMgr 	*volume.VolumePluginMgr

	sync.RWMutex
}

type nodeManaged struct {
	// nodeName contains the name of this node
	nodeName 			k8stypes.NodeName

	volumesToAttach			map[v1.UniqueVolumeName]volumeToAttach

	keepTerminatedPodVolumes 	bool
}

type volumeToAttach struct {

	multiAttachErrorReported 	bool

	volumeName 			v1.UniqueVolumeName

	spec 				*volume.Spec

	scheduledPods			map[types.UniquePodName]pod
}

type pod struct {
	// podName contains the unique identifier for this pod
	podName 			types.UniquePodName

	// pod object contains the api object of pod
	podObj 				*v1.Pod
}

type PodToAdd struct {
	Pod 				*v1.Pod
	VolumeName 			v1.UniqueVolumeName
	NodeName 			k8stypes.NodeName
}

func (dsw *desiredStateOfWorld) GetKeepTerminatedPodVolumesForNode(nodeName k8stypes.NodeName) bool {
	dsw.RLock()
	defer dsw.RUnlock()

	if nodeName == "" {
		return false
	}

	if node, ok := dsw.nodesManaged[nodeName]; ok {
		return node.keepTerminatedPodVolumes
	}

	return false
}

func (dsw *desiredStateOfWorld) AddNode(nodeName k8stypes.NodeName, keepTerminatedPodVolumes bool) {
	dsw.Lock()
	defer dsw.Unlock()

	if _, nodeExists := dsw.nodesManaged[nodeName]; !nodeExists {
		dsw.nodesManaged[nodeName] = nodeManaged{
			nodeName:                 nodeName,
			volumesToAttach:          make(map[v1.UniqueVolumeName]volumeToAttach),
			keepTerminatedPodVolumes: keepTerminatedPodVolumes,
		}
	}
}


func NewDesiredStateOfWorld(volumePluginMgr *volume.VolumePluginMgr) DesiredStateOfWorld {
	return &desiredStateOfWorld {
		nodesManaged: 		make(map[k8stypes.NodeName]nodeManaged),
		volumePluginMgr: 	volumePluginMgr,
	}
}

func (dsw *desiredStateOfWorld) GetPodToAdd() map[types.UniquePodName]PodToAdd {
	dsw.RLock()
	defer dsw.RUnlock()

	pods := make(map[types.UniquePodName]PodToAdd)
	for nodeName, nodeObj := range dsw.nodesManaged {
		for volumeName, volToAtt := range nodeObj.volumesToAttach {
			for podUID, pod := range volToAtt.scheduledPods {
				pods[podUID] = PodToAdd{
					Pod: 		pod.podObj,
					VolumeName:  	volumeName,
					NodeName: 	nodeName,
				}
			}
		}
	}
	return pods
}

func (dsw *desiredStateOfWorld) VolumeExists(
volumeName v1.UniqueVolumeName, nodeName k8stypes.NodeName) bool {
	dsw.RLock()
	defer dsw.RUnlock()

	nodeObj, nodeExists := dsw.nodesManaged[nodeName]
	if nodeExists {
		if _, volumeExists := nodeObj.volumesToAttach[volumeName]; volumeExists {
			return true
		}
	}

	return false
}

func (dsw *desiredStateOfWorld) NodeExists(nodeName k8stypes.NodeName) bool {
	dsw.RLock()
	defer dsw.RUnlock()

	_, nodeExists := dsw.nodesManaged[nodeName]
	return nodeExists
}

func (dsw *desiredStateOfWorld) AddPod(podName types.UniquePodName,
					podToAdd *v1.Pod,
					volumeSpec *volume.Spec,
					nodeName k8stypes.NodeName) (v1.UniqueVolumeName, error) {

	dsw.Lock()
	defer dsw.Unlock()

	nodeObj, nodeExists := dsw.nodesManaged[nodeName]
	if !nodeExists {
		return "", fmt.Errorf(
			"no node with the name %q exists in the list of managed nodes",
			nodeName)
	}
	// 该volumeSpec是否支持attach
	attachableVolumePlugin, err := dsw.volumePluginMgr.FindAttachablePluginBySpec(volumeSpec)
	if err != nil || attachableVolumePlugin == nil {
		if attachableVolumePlugin == nil {
			err = fmt.Errorf("plugin do not support attachment")
		}
		return "", fmt.Errorf(
			"failed to get AttachablePlugin from volumeSpec for volume %q err=%v",
			volumeSpec.Name(),
			err)
	}

	volumeName, err := util.GetUniqueVolumeNameFromSpec(attachableVolumePlugin, volumeSpec)

	if err != nil {
		return "", fmt.Errorf(
			"failed to get UniqueVolumeName from volumeSpec for plugin=%q and volume=%q err=%v",
			attachableVolumePlugin.GetPluginName(),
			volumeSpec.Name(),
			err)
	}

	klog.Infof("desiredStateOfWorld AddPod volumeName: %v", volumeName)
	//volumeName:kubernetes.io/rbd/kube:kubernetes-dynamic-pvc-795c5562-1996-11eb-b1a2-525400a3880b
	// pluginName: kubernetes.io/rbd
	// volumePlugin.GetVolumeName(volumeSpec)
	// 针对rbd 会从pv的rbdimage来组装 rbdimage为: kubernetes-dynamic-pvc-795c5562-1996-11eb-b1a2-525400a3880b
	// 所以使用的同一个image会有同一个volumeName

	// 某一个节点中volume
	// dsw.nodesManaged[node1]volumesToAttach[rbd/pvc-1]scheduledPods[podName]

	volumeObj, volumeExists := nodeObj.volumesToAttach[volumeName]
	if !volumeExists {
		volumeObj = volumeToAttach{
			multiAttachErrorReported: false,
			volumeName:               volumeName,
			spec:                     volumeSpec,
			scheduledPods:            make(map[types.UniquePodName]pod),
		}
		dsw.nodesManaged[nodeName].volumesToAttach[volumeName] = volumeObj
	}
	if _, podExists := volumeObj.scheduledPods[podName]; !podExists {
		dsw.nodesManaged[nodeName].volumesToAttach[volumeName].scheduledPods[podName] =
			pod{
				podName: podName,
				podObj:  podToAdd,
			}
	}
	return volumeName, nil
}

func (dsw *desiredStateOfWorld) DeleteNode(nodeName k8stypes.NodeName) error {
	dsw.Lock()
	defer dsw.Unlock()

	nodeObj, nodeExists := dsw.nodesManaged[nodeName]
	if !nodeExists {
		return nil
	}

	if len(nodeObj.volumesToAttach) > 0 {
		return fmt.Errorf(
			"failed to delete node %q from list of nodes managed by attach/detach controller--the node still contains %v volumes in its list of volumes to attach",
			nodeName,
			len(nodeObj.volumesToAttach))
	}

	delete(
		dsw.nodesManaged,
		nodeName)
	return nil
}

func (dsw *desiredStateOfWorld) DeletePod(
podName types.UniquePodName,
volumeName v1.UniqueVolumeName,
nodeName k8stypes.NodeName) {
	dsw.Lock()
	defer dsw.Unlock()

	nodeObj, nodeExists := dsw.nodesManaged[nodeName]
	if !nodeExists {
		return
	}

	volumeObj, volumeExists := nodeObj.volumesToAttach[volumeName]
	if !volumeExists {
		return
	}
	if _, podExists := volumeObj.scheduledPods[podName]; !podExists {
		return
	}

	delete(
		dsw.nodesManaged[nodeName].volumesToAttach[volumeName].scheduledPods,
		podName)

	if len(volumeObj.scheduledPods) == 0 {
		delete(
			dsw.nodesManaged[nodeName].volumesToAttach,
			volumeName)
	}
}

func (dsw *desiredStateOfWorld) GetVolumesToAttach() []VolumeToAttach {
	dsw.RLock()
	defer dsw.RUnlock()

	volumesToAttach := make([]VolumeToAttach, 0, len(dsw.nodesManaged))
	for nodeName, nodeObj := range dsw.nodesManaged {
		for volumeName, volumeObj := range nodeObj.volumesToAttach {
			volumesToAttach = append(volumesToAttach,
					VolumeToAttach {
						VolumeToAttach: operationexecutor.VolumeToAttach{
							MultiAttachErrorReported: 	volumeObj.multiAttachErrorReported,
							VolumeName: 			volumeName,
							VolumeSpec: 			volumeObj.spec,
							NodeName:			nodeName,
							ScheduledPods: 			getPodsFromMap(volumeObj.scheduledPods),
						}})
		}
	}

	return volumesToAttach
}

func getPodsFromMap(podMap map[types.UniquePodName]pod) []*v1.Pod {
	pods := make([]*v1.Pod, 0, len(podMap))
	for _, pod := range podMap {
		pods = append(pods, pod.podObj)
	}
	return pods
}












































































































