package kubelet_tming

import (
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet/eviction"
	"k8s.io/klog"
	"sort"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/pkg/kubelet-tming/status"
	v1qos "k8s.io/kubernetes/pkg/apis/core/v1/helper/qos"
	kubetypes "k8s.io/kubernetes/pkg/kubelet-tming/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func notRunning(statuses []v1.ContainerStatus) bool {
	for _, status := range statuses {
		if status.State.Terminated == nil && status.State.Waiting == nil {
			return false
		}
	}
	return true
}

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

func (kl *Kubelet) IsPodTerminated(uid types.UID) bool {
	pod, podFound := kl.podManager.GetPodByUID(uid)
	if !podFound {
		return true
	}
	return kl.podIsTerminated(pod)
}


func (kl *Kubelet) IsPodDeleted(uid types.UID) bool {
	pod, podFound := kl.podManager.GetPodByUID(uid)
	if !podFound {
		return true
	}

	//status, statusFound := kl.statusManager.GetPodStatus(pod.UID)
	//if !statusFound {
		status := pod.Status
	//}

	return eviction.PodIsEvicted(status) || (pod.DeletionTimestamp != nil && notRunning(status.ContainerStatuses))
}

// generateAPIPodStatus creates the final API pod status for a pod, given the
// internal pod status.
func (kl *Kubelet) generateAPIPodStatus(pod *v1.Pod, podStatus *kubecontainer.PodStatus) v1.PodStatus {
	klog.V(3).Infof("Generating status for %q", format.Pod(pod))

	s := kl.convertStatusToAPIStatus(pod, podStatus)

	// check if an internal module has requested the pod is evicted.
	//for _, podSyncHandler := range kl.PodSyncHandlers {
	//	if result := podSyncHandler.ShouldEvict(pod); result.Evict {
	//		s.Phase = v1.PodFailed
	//		s.Reason = result.Reason
	//		s.Message = result.Message
	//		return *s
	//	}
	//}

	// Assume info is ready to process
	spec := &pod.Spec
	allStatus := append(append([]v1.ContainerStatus{}, s.ContainerStatuses...), s.InitContainerStatuses...)
	s.Phase = getPhase(spec, allStatus)
	// Check for illegal phase transition
	if pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodSucceeded {
		// API server shows terminal phase; transitions are not allowed
		if s.Phase != pod.Status.Phase {
			klog.Errorf("Pod attempted illegal phase transition from %s to %s: %v", pod.Status.Phase, s.Phase, s)
			// Force back to phase from the API server
			s.Phase = pod.Status.Phase
		}
	}

	// TODO
	//kl.probeManager.UpdatePodStatus(pod.UID, s)

	s.Conditions = append(s.Conditions, status.GeneratePodInitializedCondition(spec, s.InitContainerStatuses, s.Phase))
	s.Conditions = append(s.Conditions, status.GeneratePodReadyCondition(spec, s.Conditions, s.ContainerStatuses, s.Phase))
	s.Conditions = append(s.Conditions, status.GenerateContainersReadyCondition(spec, s.ContainerStatuses, s.Phase))
	// Status manager will take care of the LastTransitionTimestamp, either preserve
	// the timestamp from apiserver, or set a new one. When kubelet sees the pod,
	// `PodScheduled` condition must be true.
	s.Conditions = append(s.Conditions, v1.PodCondition{
		Type:   v1.PodScheduled,
		Status: v1.ConditionTrue,
	})

	if kl.kubeClient != nil {
		hostIP, err := kl.getHostIPAnyWay()
		if err != nil {
			klog.V(4).Infof("Cannot get host IP: %v", err)
		} else {
			s.HostIP = hostIP.String()
			if kubecontainer.IsHostNetworkPod(pod) && s.PodIP == "" {
				s.PodIP = hostIP.String()
			}
		}
	}

	return *s
}

// convertStatusToAPIStatus creates an api PodStatus for the given pod from
// the given internal pod status.  It is purely transformative and does not
// alter the kubelet state at all.
func (kl *Kubelet) convertStatusToAPIStatus(pod *v1.Pod, podStatus *kubecontainer.PodStatus) *v1.PodStatus {
	var apiPodStatus v1.PodStatus
	apiPodStatus.PodIP = podStatus.IP
	// set status for Pods created on versions of kube older than 1.6
	apiPodStatus.QOSClass = v1qos.GetPodQOS(pod)

	oldPodStatus, found := kl.statusManager.GetPodStatus(pod.UID)
	if !found {
		oldPodStatus = pod.Status
	}

	apiPodStatus.ContainerStatuses = kl.convertToAPIContainerStatuses(
		pod, podStatus,
		oldPodStatus.ContainerStatuses,
		pod.Spec.Containers,
		len(pod.Spec.InitContainers) > 0,
		false,
	)
	apiPodStatus.InitContainerStatuses = kl.convertToAPIContainerStatuses(
		pod, podStatus,
		oldPodStatus.InitContainerStatuses,
		pod.Spec.InitContainers,
		len(pod.Spec.InitContainers) > 0,
		true,
	)

	// Preserves conditions not controlled by kubelet
	for _, c := range pod.Status.Conditions {
		if !kubetypes.PodConditionByKubelet(c.Type) {
			apiPodStatus.Conditions = append(apiPodStatus.Conditions, c)
		}
	}
	return &apiPodStatus
}

// convertToAPIContainerStatuses converts the given internal container
// statuses into API container statuses.
func (kl *Kubelet) convertToAPIContainerStatuses(pod *v1.Pod, podStatus *kubecontainer.PodStatus, previousStatus []v1.ContainerStatus, containers []v1.Container, hasInitContainers, isInitContainer bool) []v1.ContainerStatus {
	convertContainerStatus := func(cs *kubecontainer.ContainerStatus) *v1.ContainerStatus {
		cid := cs.ID.String()
		status := &v1.ContainerStatus{
			Name:         cs.Name,
			RestartCount: int32(cs.RestartCount),
			Image:        cs.Image,
			ImageID:      cs.ImageID,
			ContainerID:  cid,
		}
		switch cs.State {
		case kubecontainer.ContainerStateRunning:
			status.State.Running = &v1.ContainerStateRunning{StartedAt: metav1.NewTime(cs.StartedAt)}
		case kubecontainer.ContainerStateCreated:
			// Treat containers in the "created" state as if they are exited.
			// The pod workers are supposed start all containers it creates in
			// one sync (syncPod) iteration. There should not be any normal
			// "created" containers when the pod worker generates the status at
			// the beginning of a sync iteration.
			fallthrough
		case kubecontainer.ContainerStateExited:
			status.State.Terminated = &v1.ContainerStateTerminated{
				ExitCode:    int32(cs.ExitCode),
				Reason:      cs.Reason,
				Message:     cs.Message,
				StartedAt:   metav1.NewTime(cs.StartedAt),
				FinishedAt:  metav1.NewTime(cs.FinishedAt),
				ContainerID: cid,
			}
		default:
			status.State.Waiting = &v1.ContainerStateWaiting{}
		}
		return status
	}

	// Fetch old containers statuses from old pod status.
	oldStatuses := make(map[string]v1.ContainerStatus, len(containers))
	for _, status := range previousStatus {
		oldStatuses[status.Name] = status
	}

	// Set all container statuses to default waiting state
	statuses := make(map[string]*v1.ContainerStatus, len(containers))
	defaultWaitingState := v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "ContainerCreating"}}
	if hasInitContainers {
		defaultWaitingState = v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "PodInitializing"}}
	}

	for _, container := range containers {
		status := &v1.ContainerStatus{
			Name:  container.Name,
			Image: container.Image,
			State: defaultWaitingState,
		}
		oldStatus, found := oldStatuses[container.Name]
		if found {
			if oldStatus.State.Terminated != nil {
				// Do not update status on terminated init containers as
				// they be removed at any time.
				status = &oldStatus
			} else {
				// Apply some values from the old statuses as the default values.
				status.RestartCount = oldStatus.RestartCount
				status.LastTerminationState = oldStatus.LastTerminationState
			}
		}
		statuses[container.Name] = status
	}

	// Make the latest container status comes first.
	sort.Sort(sort.Reverse(kubecontainer.SortContainerStatusesByCreationTime(podStatus.ContainerStatuses)))
	// Set container statuses according to the statuses seen in pod status
	containerSeen := map[string]int{}
	for _, cStatus := range podStatus.ContainerStatuses {
		cName := cStatus.Name
		if _, ok := statuses[cName]; !ok {
			// This would also ignore the infra container.
			continue
		}
		if containerSeen[cName] >= 2 {
			continue
		}
		status := convertContainerStatus(cStatus)
		if containerSeen[cName] == 0 {
			statuses[cName] = status
		} else {
			statuses[cName].LastTerminationState = status.State
		}
		containerSeen[cName] = containerSeen[cName] + 1
	}

	// Handle the containers failed to be started, which should be in Waiting state.
	for _, container := range containers {
		if isInitContainer {
			// If the init container is terminated with exit code 0, it won't be restarted.
			// TODO(random-liu): Handle this in a cleaner way.
			s := podStatus.FindContainerStatusByName(container.Name)
			if s != nil && s.State == kubecontainer.ContainerStateExited && s.ExitCode == 0 {
				continue
			}
		}
		// If a container should be restarted in next syncpod, it is *Waiting*.
		if !kubecontainer.ShouldContainerBeRestarted(&container, pod, podStatus) {
			continue
		}
		status := statuses[container.Name]
		//reason, ok := kl.reasonCache.Get(pod.UID, container.Name)
		//if !ok {
		//	// In fact, we could also apply Waiting state here, but it is less informative,
		//	// and the container will be restarted soon, so we prefer the original state here.
		//	// Note that with the current implementation of ShouldContainerBeRestarted the original state here
		//	// could be:
		//	//   * Waiting: There is no associated historical container and start failure reason record.
		//	//   * Terminated: The container is terminated.
		//	continue
		//}
		if status.State.Terminated != nil {
			status.LastTerminationState = status.State
		}
		//status.State = v1.ContainerState{
		//	Waiting: &v1.ContainerStateWaiting{
		//		Reason:  reason.Err.Error(),
		//		Message: reason.Message,
		//	},
		//}
		statuses[container.Name] = status
	}

	var containerStatuses []v1.ContainerStatus
	for _, status := range statuses {
		containerStatuses = append(containerStatuses, *status)
	}

	// Sort the container statuses since clients of this interface expect the list
	// of containers in a pod has a deterministic order.
	if isInitContainer {
		kubetypes.SortInitContainerStatuses(pod, containerStatuses)
	} else {
		sort.Sort(kubetypes.SortedContainerStatuses(containerStatuses))
	}
	return containerStatuses
}

// getPhase returns the phase of a pod given its container info.
func getPhase(spec *v1.PodSpec, info []v1.ContainerStatus) v1.PodPhase {
	pendingInitialization := 0
	failedInitialization := 0
	for _, container := range spec.InitContainers {
		containerStatus, ok := podutil.GetContainerStatus(info, container.Name)
		if !ok {
			pendingInitialization++
			continue
		}

		switch {
		case containerStatus.State.Running != nil:
			pendingInitialization++
		case containerStatus.State.Terminated != nil:
			if containerStatus.State.Terminated.ExitCode != 0 {
				failedInitialization++
			}
		case containerStatus.State.Waiting != nil:
			if containerStatus.LastTerminationState.Terminated != nil {
				if containerStatus.LastTerminationState.Terminated.ExitCode != 0 {
					failedInitialization++
				}
			} else {
				pendingInitialization++
			}
		default:
			pendingInitialization++
		}
	}

	unknown := 0
	running := 0
	waiting := 0
	stopped := 0
	succeeded := 0
	for _, container := range spec.Containers {
		containerStatus, ok := podutil.GetContainerStatus(info, container.Name)
		if !ok {
			unknown++
			continue
		}

		switch {
		case containerStatus.State.Running != nil:
			running++
		case containerStatus.State.Terminated != nil:
			stopped++
			if containerStatus.State.Terminated.ExitCode == 0 {
				succeeded++
			}
		case containerStatus.State.Waiting != nil:
			if containerStatus.LastTerminationState.Terminated != nil {
				stopped++
			} else {
				waiting++
			}
		default:
			unknown++
		}
	}

	if failedInitialization > 0 && spec.RestartPolicy == v1.RestartPolicyNever {
		return v1.PodFailed
	}

	switch {
	case pendingInitialization > 0:
		fallthrough
	case waiting > 0:
		klog.V(5).Infof("pod waiting > 0, pending")
		// One or more containers has not been started
		return v1.PodPending
	case running > 0 && unknown == 0:
		// All containers have been started, and at least
		// one container is running
		return v1.PodRunning
	case running == 0 && stopped > 0 && unknown == 0:
		// All containers are terminated
		if spec.RestartPolicy == v1.RestartPolicyAlways {
			// All containers are in the process of restarting
			return v1.PodRunning
		}
		if stopped == succeeded {
			// RestartPolicy is not Always, and all
			// containers are terminated in success
			return v1.PodSucceeded
		}
		if spec.RestartPolicy == v1.RestartPolicyNever {
			// RestartPolicy is Never, and all containers are
			// terminated with at least one in failure
			return v1.PodFailed
		}
		// RestartPolicy is OnFailure, and at least one in failure
		// and in the process of restarting
		return v1.PodRunning
	default:
		klog.V(5).Infof("pod default case, pending")
		return v1.PodPending
	}
}

