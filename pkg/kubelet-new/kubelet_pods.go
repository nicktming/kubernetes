package kubelet_new

import (
	"k8s.io/api/core/v1"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	"k8s.io/apimachinery/pkg/types"
	"os"
	"fmt"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet-new/status"
	"k8s.io/kubernetes/pkg/kubelet-new/cm"
	"k8s.io/apimachinery/pkg/util/sets"
)

func getPhase(spec *v1.PodSpec, info []v1.ContainerStatus) v1.PodPhase {
	waiting := 0
	running := 0
	terminated := 0
	failed := 0
	for _, cs := range info {
		if cs.State.Running != nil {
			running++
		} else if cs.State.Terminated != nil {
			terminated++
			if cs.State.Terminated.ExitCode != 0 {
				failed++
			}
		} else {
			waiting++
		}
	}
	if running > 0 && failed == 0 {
		return v1.PodRunning
	}
	if running == 0 && waiting == 0 {
		if failed > 0 {
			if spec.RestartPolicy == v1.RestartPolicyNever {
				return v1.PodFailed
			}
		} else {
			if spec.RestartPolicy != v1.RestartPolicyAlways {
				return v1.PodSucceeded
			}
		}
	}
	return v1.PodPending
}

func (kl *Kubelet) generateAPIPodStatus(pod *v1.Pod, podStatus *kubecontainer.PodStatus) v1.PodStatus {
	s := kl.convertToAPIContainerStatuses(pod, podStatus)
	allStatus := append(append([]v1.ContainerStatus{}, s.ContainerStatuses...), s.InitContainerStatuses...)

	spec := &pod.Spec
	s.Phase = getPhase(spec, allStatus)

	kl.probeManager.UpdatePodStatus(pod.UID, s)
	s.Conditions = append(s.Conditions, status.GeneratePodInitializedCondition(spec, s.InitContainerStatuses, s.Phase))
	s.Conditions = append(s.Conditions, status.GeneratePodReadyCondition(spec, s.Conditions, s.ContainerStatuses, s.Phase))
	s.Conditions = append(s.Conditions, status.GenerateContainersReadyCondition(spec, s.ContainerStatuses, s.Phase))

	s.Conditions = append(s.Conditions, v1.PodCondition{
		Type: 	v1.PodScheduled,
		Status: v1.ConditionTrue,
	})
	return *s
}

func (kl *Kubelet) convertToAPIContainerStatuses(pod *v1.Pod, podStatus *kubecontainer.PodStatus) *v1.PodStatus {
	var apiPodStatus v1.PodStatus

	if podStatus == nil {
		return &apiPodStatus
	}

	covertContainerStatus := func(cs *kubecontainer.ContainerStatus) v1.ContainerStatus {
		cid := cs.ID.String()
		status := v1.ContainerStatus{
			Name: 			cs.Name,
			RestartCount: 		int32(cs.RestartCount),
			Image: 			cs.Image,
			ImageID: 		cs.ImageID,
			ContainerID: 		cid,
		}
		switch cs.State {
		case kubecontainer.ContainerStateRunning:
			status.State.Running = &v1.ContainerStateRunning{
				StartedAt: metav1.NewTime(cs.StartedAt),
			}
		case kubecontainer.ContainerStateCreated:
			fallthrough
		case kubecontainer.ContainerStateExited:
			status.State.Terminated = &v1.ContainerStateTerminated{
				ExitCode: 	int32(cs.ExitCode),
				Reason: 	cs.Reason,
				Message: 	cs.Message,
				StartedAt: 	metav1.NewTime(cs.StartedAt),
				FinishedAt:  	metav1.NewTime(cs.FinishedAt),
				ContainerID: 	cid,
			}
		default:
			status.State.Waiting = &v1.ContainerStateWaiting{}
		}
		return status
	}

	apiPodStatus.ContainerStatuses = make([]v1.ContainerStatus, len(podStatus.ContainerStatuses))

	for i, containerStatus := range podStatus.ContainerStatuses {
		apiPodStatus.ContainerStatuses[i] = covertContainerStatus(containerStatus)
	}

	return &apiPodStatus
}

// makePodDataDirs creates the dirs for the pod datas.
func (kl *Kubelet) makePodDataDirs(pod *v1.Pod) error {
	uid := pod.UID
	if err := os.MkdirAll(kl.getPodDir(uid), 0750); err != nil && !os.IsExist(err) {
		return err
	}
	if err := os.MkdirAll(kl.getPodVolumesDir(uid), 0750); err != nil && !os.IsExist(err) {
		return err
	}
	if err := os.MkdirAll(kl.getPodPluginsDir(uid), 0750); err != nil && !os.IsExist(err) {
		return err
	}
	return nil
}


func (kl *Kubelet) killPod(pod *v1.Pod, runningPod *kubecontainer.Pod, status *kubecontainer.PodStatus, gracePeriodOverride *int64) error {
	var p kubecontainer.Pod
	if runningPod != nil {
		p = *runningPod
	} else if status != nil {
		p = kubecontainer.ConvertPodStatusToRunningPod(kl.getRuntime().Type(), status)
	} else {
		return fmt.Errorf("one of the two arguments must be non-nil: runningPod, status")
	}
	// Call the container runtime KillPod method which stops all running containers of the pod
	if err := kl.containerRuntime.KillPod(pod, p, gracePeriodOverride); err != nil {
		return err
	}
	// qos cgroup
	return nil
}

func (kl *Kubelet) PodResourcesAreReclaimed(pod *v1.Pod, status v1.PodStatus) bool {
	if pod.DeletionTimestamp == nil || !notRunning(status.ContainerStatuses) {
		return false
	}
	// pod's containers should be deleted
	runtimeStatus, err := kl.podCache.Get(pod.UID)
	if err != nil {
		klog.Infof("Pod %q is terminated, Error getting runtimeStatus from the podCache: %s", format.Pod(pod), err)
		return false
	}
	if runtimeStatus != nil && len(runtimeStatus.ContainerStatuses) > 0 {
		var statusStr string
		for _, status := range runtimeStatus.ContainerStatuses {
			statusStr += fmt.Sprintf("%+v ", *status)
		}
		klog.Infof("Pod %q is terminated, but some containers have not been cleaned up: %s", format.Pod(pod), statusStr)
		return false
	}
	// TODO podVolumesExist keepTerminatedPodVolumes
	if kl.podVolumesExist(pod.UID) {
		klog.Infof("Pod %q is terminated, but some volumes have not been cleaned up", format.Pod(pod))
		return false
	}

	// 判断该pod的cgroup是否清除 待研究
	if kl.kubeletConfiguration.CgroupsPerQOS {
		pcm := kl.containerManager.NewPodContainerManager()
		if pcm.Exists(pod) {
			klog.V(3).Infof("Pod %q is terminated, but pod cgroup sandbox has not been cleaned up", format.Pod(pod))
			return false
		}
	}

	return true
}

func notRunning(statuses []v1.ContainerStatus) bool {
	for _, status := range statuses {
		if status.State.Terminated == nil && status.State.Waiting == nil {
			return false
		}
	}
	return true
}


func (kl *Kubelet) HandlePodCleanups() error {
	var (
		cgroupPods map[types.UID]cm.CgroupName
		err        error
	)

	if kl.cgroupsPerQOS {
		pcm := kl.containerManager.NewPodContainerManager()
		cgroupPods, err = pcm.GetAllPodsFromCgroups()
		if err != nil {
			return fmt.Errorf("failed to get list of pods that still exist on cgroup mounts: %v", err)
		}
	}

	allPods, _ := kl.podManager.GetPodsAndMirrorPods()
	// Pod phase progresses monotonically. Once a pod has reached a final state,
	// it should never leave regardless of the restart policy. The statuses
	// of such pods should not be changed, and there is no need to sync them.
	// TODO: the logic here does not handle two cases:
	//   1. If the containers were removed immediately after they died, kubelet
	//      may fail to generate correct statuses, let alone filtering correctly.
	//   2. If kubelet restarted before writing the terminated status for a pod
	//      to the apiserver, it could still restart the terminated pod (even
	//      though the pod was not considered terminated by the apiserver).
	// These two conditions could be alleviated by checkpointing kubelet.
	activePods := kl.filterOutTerminatedPods(allPods)

	// Remove any cgroups in the hierarchy for pods that are no longer running.
	if kl.cgroupsPerQOS {
		kl.cleanupOrphanedPodCgroups(cgroupPods, activePods)
	}

	return nil
}

// 1. 从cgroup找到所有的cgroupPods, 从podmanager过滤出所有的activepods
// 2. 遍历cgroupPods, 发现不在activepods中,
// 2.1 如果pod存在mounted volume, 把该pod的cgroup调整到最小
// 2.2 如果pod不存在mounted volume, 则把该cgroup下所有的pid全部kill并且从cgroup的目录路径中删除

// cleanupOrphanedPodCgroups removes cgroups that should no longer exist.
// it reconciles the cached state of cgroupPods with the specified list of runningPods
func (kl *Kubelet) cleanupOrphanedPodCgroups(cgroupPods map[types.UID]cm.CgroupName, activePods []*v1.Pod) {
	// Add all running pods to the set that we want to preserve
	podSet := sets.NewString()
	for _, pod := range activePods {
		podSet.Insert(string(pod.UID))
	}
	pcm := kl.containerManager.NewPodContainerManager()
	// Iterate over all the found pods to verify if they should be running
	for uid, val := range cgroupPods {
		// if the pod is in the running set, its not a candidate for cleanup
		if podSet.Has(string(uid)) {
			continue
		}

		// 如果该pod仍然存在mounted volumes, 不删除该pod的cgroup, 而是把该cgroup的内容调整到最小, cpushare为2, 其余为默认值

		// If volumes have not been unmounted/detached, do not delete the cgroup
		// so any memory backed volumes don't have their charges propagated to the
		// parent croup.  If the volumes still exist, reduce the cpu shares for any
		// process in the cgroup to the minimum value while we wait.  if the kubelet
		// is configured to keep terminated volumes, we will delete the cgroup and not block.
		//  && !kl.keepTerminatedPodVolumes
		if podVolumesExist := kl.podVolumesExist(uid); podVolumesExist {
			klog.V(3).Infof("Orphaned pod %q found, but volumes not yet removed.  Reducing cpu to minimum", uid)
			if err := pcm.ReduceCPULimits(val); err != nil {
				klog.Warningf("Failed to reduce cpu time for pod %q pending volume cleanup due to %v", uid, err)
			}
			continue
		}
		klog.V(3).Infof("Orphaned pod %q found, removing pod cgroups", uid)
		// Destroy all cgroups of pod that should not be running,
		// by first killing all the attached processes to these cgroups.
		// We ignore errors thrown by the method, as the housekeeping loop would
		// again try to delete these unwanted pod cgroups
		go pcm.Destroy(val)
	}

}

// IsPodTerminated returns true if the pod with the provided UID is in a terminated state ("Failed" or "Succeeded")
// or if the pod has been deleted or removed
//func (kl *Kubelet) IsPodTerminated(uid types.UID) bool {
//	pod, podFound := kl.podManager.GetPodByUID(uid)
//	if !podFound {
//		return true
//	}
//	return kl.podIsTerminated(pod)
//}

// podIsTerminated returns true if pod is in the terminated state ("Failed" or "Succeeded").
func (kl *Kubelet) podIsTerminated(pod *v1.Pod) bool {
	// Check the cached pod status which was set after the last sync.
	status, ok := kl.statusManager.GetPodStatus(pod.UID)
	if !ok {
		// If there is no cached status, use the status from the
		// apiserver. This is useful if kubelet has recently been
		// restarted.
		status = pod.Status
	}
	return status.Phase == v1.PodFailed || status.Phase == v1.PodSucceeded || (pod.DeletionTimestamp != nil && notRunning(status.ContainerStatuses))
}

func (kl *Kubelet) IsPodDeleted(uid types.UID) bool {
	pod, podFound := kl.podManager.GetPodByUID(uid)
	if !podFound {
		return true
	}
	status, statusFound := kl.statusManager.GetPodStatus(uid)
	if !statusFound {
		status = pod.Status
	}
	// TODO status evicted
	return pod.DeletionTimestamp != nil && notRunning(status.ContainerStatuses)
}

func (kl *Kubelet) filterOutTerminatedPods(pods []*v1.Pod) []*v1.Pod {
	var filteredPods []*v1.Pod
	for _, p := range pods {
		if kl.podIsTerminated(p) {
			continue
		}
		filteredPods = append(filteredPods, p)
	}
	return filteredPods
}

// GetActivePods returns non-terminal pods
func (kl *Kubelet) GetActivePods() []*v1.Pod {
	allPods := kl.podManager.GetPods()
	activePods := kl.filterOutTerminatedPods(allPods)
	return activePods
}

// GetPodCgroupParent gets pod cgroup parent from container manager.
func (kl *Kubelet) GetPodCgroupParent(pod *v1.Pod) string {
	pcm := kl.containerManager.NewPodContainerManager()
	_, cgroupParent := pcm.GetPodContainerName(pod)
	return cgroupParent
}
















































