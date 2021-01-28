package kubelet_new

import (
	"k8s.io/api/core/v1"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		}
		return status
	}

	apiPodStatus.ContainerStatuses = make([]v1.ContainerStatus, len(podStatus.ContainerStatuses))

	for i, containerStatus := range podStatus.ContainerStatuses {
		apiPodStatus.ContainerStatuses[i] = covertContainerStatus(containerStatus)
	}

	return apiPodStatus
}