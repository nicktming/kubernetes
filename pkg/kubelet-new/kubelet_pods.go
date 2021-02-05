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
	return s
}

func (kl *Kubelet) convertToAPIContainerStatuses(pod *v1.Pod, podStatus *kubecontainer.PodStatus) v1.PodStatus {
	var apiPodStatus v1.PodStatus

	if podStatus == nil {
		return apiPodStatus
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

	return apiPodStatus
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
	// TODO cgroup qos
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

func (kl *Kubelet) getMountedVolumePathListFromDisk(podUID types.UID) ([]string, error) {
	mountedVolumes := []string{}
	volumePaths, err := kl.getPodVolumePathListFromDisk(podUID)
	if err != nil {
		return mountedVolumes, err
	}
	//for _, volumePath := range volumePaths {
	//	isNotMount, err :=
	//}
	return volumePaths, nil
}


func (kl *Kubelet) HandlePodCleanups() error {
	return nil
}














































