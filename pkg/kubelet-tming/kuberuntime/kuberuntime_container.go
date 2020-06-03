package kuberuntime


import (
	"k8s.io/klog"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"sort"
	kubetypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet-tming/types"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	"time"
	v1 "k8s.io/api/core/v1"
	"fmt"
	"math/rand"
	"k8s.io/kubernetes/pkg/util/tail"
	"os"
	"github.com/armon/circbuf"
	"context"
	"sync"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	"k8s.io/gengo/examples/set-gen/sets"
)

// removeContainer removes the container and the container logs.
// Notice that we remove the container logs first, so that container will not be removed if
// container logs are failed to be removed, and kubelet will retry this later. This guarantees
// that container logs to be removed with the container.
// Notice that we assume that the container should only be removed in non-running state, and
// it will not write container logs anymore in that state.
func (m *kubeGenericRuntimeManager) removeContainer(containerID string) error {
	klog.V(4).Infof("Removing container %q", containerID)
	// TODO

	// Call internal container post-stop lifecycle hook.
	//if err := m.internalLifecycle.PostStopContainer(containerID); err != nil {
	//	return err
	//}

	// Remove the container log.
	// TODO: Separate log and container lifecycle management.
	if err := m.removeContainerLog(containerID); err != nil {
		return err
	}
	// Remove the container.
	return m.runtimeService.RemoveContainer(containerID)
}

// removeContainerLog removes the container log.
func (m *kubeGenericRuntimeManager) removeContainerLog(containerID string) error {
	// Remove the container log.
	status, err := m.runtimeService.ContainerStatus(containerID)
	if err != nil {
		return fmt.Errorf("failed to get container status %q: %v", containerID, err)
	}
	labeledInfo := getContainerInfoFromLabels(status.Labels)
	path := status.GetLogPath()

	// TODO path

	//if err := m.osInterface.Remove(path); err != nil && !os.IsNotExist(err) {
	//	return fmt.Errorf("failed to remove container %q log %q: %v", containerID, path, err)
	//}

	// Remove the legacy container log symlink.
	// TODO(random-liu): Remove this after cluster logging supports CRI container log path.
	//legacySymlink := legacyLogSymlink(containerID, labeledInfo.ContainerName, labeledInfo.PodName,
	//	labeledInfo.PodNamespace)
	//if err := m.osInterface.Remove(legacySymlink); err != nil && !os.IsNotExist(err) {
	//	return fmt.Errorf("failed to remove container %q log legacy symbolic link %q: %v",
	//		containerID, legacySymlink, err)
	//}
	return nil
}

// DeleteContainer removes a container.
func (m *kubeGenericRuntimeManager) DeleteContainer(containerID kubecontainer.ContainerID) error {
	return m.removeContainer(containerID.ID)
}


func (m *kubeGenericRuntimeManager) purgeInitContainers(pod *v1.Pod, podStatus *kubecontainer.PodStatus) {
	initContainerNames := sets.NewString()
	for _, container := range pod.Spec.InitContainers {
		initContainerNames.Insert(container.Name)
	}
	for name := range initContainerNames {
		count := 0
		for _, status := range podStatus.ContainerStatuses {
			if status.Name != name || !initContainerNames.Has(status.Name) {
				continue
			}

			count++
			klog.Infof("Removing init container %q instance %q %d", status.Name, status.ID.ID, count)

			if err := m.removeContainer(status.ID.ID); err != nil {
				utilruntime.HandleError(fmt.Errorf("failed to remove pod init container %q: %v; Skipping pod %q", status.Name, err, format.Pod(pod)))
				continue
			}
			// Remove any references to this container
			//if _, ok := m.containerRefManager.GetRef(status.ID); ok {
			//	m.containerRefManager.ClearRef(status.ID)
			//} else {
			//	klog.Warningf("No ref for container %q", status.ID)
			//}
		}
	}
}

func (m *kubeGenericRuntimeManager) killContainer(pod *v1.Pod, containerID kubecontainer.ContainerID, containerName string, message string, gracePeriodOverride *int64) error {
	var containerSpec *v1.Container
	if pod != nil {
		if containerSpec = kubecontainer.GetContainerSpec(pod, containerName); containerSpec == nil {
			return fmt.Errorf("failed to get containerSpec %q(id=%q) in pod %q when killing container for reason %q",
				containerName, containerID.String(), format.Pod(pod), message)
		}
	} else {
		//TODO restore from runtimeService
	}

	gracePeriod := int64(minimumGracePeriodInSeconds)
	switch {
	case pod.DeletionGracePeriodSeconds != nil:
		gracePeriod = *pod.DeletionGracePeriodSeconds
	case pod.Spec.TerminationGracePeriodSeconds != nil:
		gracePeriod = *pod.Spec.TerminationGracePeriodSeconds
	}

	if len(message) == 0 {
		message = fmt.Sprintf("Stopping container %s", containerSpec.Name)
	}

	//m.recordContainerEvent(pod, containerSpec, containerID.ID, v1.EventTypeNormal, events.KillingContainer, message)


	//// Run internal pre-stop lifecycle hook
	//if err := m.internalLifecycle.PreStopContainer(containerID.ID); err != nil {
	//	return err
	//}
	//
	//// Run the pre-stop lifecycle hooks if applicable and if there is enough time to run it
	//if containerSpec.Lifecycle != nil && containerSpec.Lifecycle.PreStop != nil && gracePeriod > 0 {
	//	gracePeriod = gracePeriod - m.executePreStopHook(pod, containerID, containerSpec, gracePeriod)
	//}
	//// always give containers a minimal shutdown window to avoid unnecessary SIGKILLs
	//if gracePeriod < minimumGracePeriodInSeconds {
	//	gracePeriod = minimumGracePeriodInSeconds
	//}

	if gracePeriodOverride != nil {
		gracePeriod = *gracePeriodOverride
		klog.Infof("Killing container %q, but using %d second grace period override", containerID, gracePeriod)
	}

	klog.Infof("Killing container %q with %d second grace period", containerID.String(), gracePeriod)

	err := m.runtimeService.StopContainer(containerID.ID, gracePeriod)
	if err != nil {
		klog.Errorf("Container %q termination failed with gracePeriod %d: %v", containerID.String(), gracePeriod, err)
	} else {
		klog.Infof("Container %q exited normally", containerID.String())
	}

	// TODO containerRefManager
	//m.containerRefManager.ClearRef(containerID)

	return err
}


func (m *kubeGenericRuntimeManager) killContainersWithSyncResult(pod *v1.Pod, runningPod kubecontainer.Pod, gracePeriodOverride *int64) (syncResults []*kubecontainer.SyncResult) {
	containerResults := make(chan *kubecontainer.SyncResult, len(runningPod.Containers))
	wg := sync.WaitGroup{}

	wg.Add(len(runningPod.Containers))

	for _, container := range runningPod.Containers {

		go func (container kubecontainer.Container) {
			defer utilruntime.HandleCrash()
			defer wg.Done()

			killContainerResult := kubecontainer.NewSyncResult(kubecontainer.KillContainer, container.Name)

			if err := m.killContainer(pod, container.ID, container.Name, "", gracePeriodOverride); err != nil {
				killContainerResult.Fail(kubecontainer.ErrKillContainer, err.Error())
			}
			containerResults <- killContainerResult
		}(container)

	}

	wg.Wait()

	close(containerResults)

	for containerResults := range containerResults {
		syncResults = append(syncResults, containerResults)
	}
	return
}

func (m *kubeGenericRuntimeManager) getKubeletContainers(allContainers bool) ([]*runtimeapi.Container, error) {
	filter := &runtimeapi.ContainerFilter{}

	if !allContainers {
		filter.State = &runtimeapi.ContainerStateValue{
			State:		runtimeapi.ContainerState_CONTAINER_RUNNING,
		}
	}

	containers, err := m.runtimeService.ListContainers(filter)
	if err != nil {
		klog.Errorf("getKubeletContainers failed: %v", err)
		return nil, err
	}

	return containers, nil
}

// getPodContainerStatuses gets all containers' statuses for the pod.
func (m *kubeGenericRuntimeManager) getPodContainerStatuses(uid kubetypes.UID, name, namespace string) ([]*kubecontainer.ContainerStatus, error) {
	// Select all containers of the given pod.
	containers, err := m.runtimeService.ListContainers(&runtimeapi.ContainerFilter{
		LabelSelector: map[string]string{types.KubernetesPodUIDLabel: string(uid)},
	})
	if err != nil {
		klog.Errorf("ListContainers error: %v", err)
		return nil, err
	}

	statuses := make([]*kubecontainer.ContainerStatus, len(containers))
	// TODO: optimization: set maximum number of containers per container name to examine.
	for i, c := range containers {
		status, err := m.runtimeService.ContainerStatus(c.Id)
		if err != nil {
			// Merely log this here; GetPodStatus will actually report the error out.
			klog.V(4).Infof("ContainerStatus for %s error: %v", c.Id, err)
			return nil, err
		}
		cStatus := toKubeContainerStatus(status, m.runtimeName)
		if status.State == runtimeapi.ContainerState_CONTAINER_EXITED {
			// Populate the termination message if needed.
			annotatedInfo := getContainerInfoFromAnnotations(status.Annotations)
			fallbackToLogs := annotatedInfo.TerminationMessagePolicy == v1.TerminationMessageFallbackToLogsOnError && cStatus.ExitCode != 0
			tMessage, checkLogs := getTerminationMessage(status, annotatedInfo.TerminationMessagePath, fallbackToLogs)
			if checkLogs {
				// if dockerLegacyService is populated, we're supposed to use it to fetch logs
				if m.legacyLogProvider != nil {
					tMessage, err = m.legacyLogProvider.GetContainerLogTail(uid, name, namespace, kubecontainer.ContainerID{Type: m.runtimeName, ID: c.Id})
					if err != nil {
						tMessage = fmt.Sprintf("Error reading termination message from logs: %v", err)
					}
				} else {
					tMessage = m.readLastStringFromContainerLogs(status.GetLogPath())
				}
			}
			// Use the termination message written by the application is not empty
			if len(tMessage) != 0 {
				cStatus.Message = tMessage
			}
		}
		statuses[i] = cStatus
	}

	sort.Sort(containerStatusByCreated(statuses))
	return statuses, nil
}

// readLastStringFromContainerLogs attempts to read up to the max log length from the end of the CRI log represented
// by path. It reads up to max log lines.
func (m *kubeGenericRuntimeManager) readLastStringFromContainerLogs(path string) string {
	value := int64(kubecontainer.MaxContainerTerminationMessageLogLines)
	buf, _ := circbuf.NewBuffer(kubecontainer.MaxContainerTerminationMessageLogLength)
	if err := m.ReadLogs(context.Background(), path, "", &v1.PodLogOptions{TailLines: &value}, buf, buf); err != nil {
		return fmt.Sprintf("Error on reading termination message from logs: %v", err)
	}
	return buf.String()
}


func toKubeContainerStatus(status *runtimeapi.ContainerStatus, runtimeName string) *kubecontainer.ContainerStatus {
	annotatedInfo := getContainerInfoFromAnnotations(status.Annotations)
	labeledInfo := getContainerInfoFromLabels(status.Labels)
	cStatus := &kubecontainer.ContainerStatus{
		ID: kubecontainer.ContainerID{
			Type: runtimeName,
			ID:   status.Id,
		},
		Name:         labeledInfo.ContainerName,
		Image:        status.Image.Image,
		ImageID:      status.ImageRef,
		Hash:         annotatedInfo.Hash,
		RestartCount: annotatedInfo.RestartCount,
		State:        toKubeContainerState(status.State),
		CreatedAt:    time.Unix(0, status.CreatedAt),
	}

	if status.State != runtimeapi.ContainerState_CONTAINER_CREATED {
		// If container is not in the created state, we have tried and
		// started the container. Set the StartedAt time.
		cStatus.StartedAt = time.Unix(0, status.StartedAt)
	}
	if status.State == runtimeapi.ContainerState_CONTAINER_EXITED {
		cStatus.Reason = status.Reason
		cStatus.Message = status.Message
		cStatus.ExitCode = int(status.ExitCode)
		cStatus.FinishedAt = time.Unix(0, status.FinishedAt)
	}
	return cStatus
}

// makeUID returns a randomly generated string.
func makeUID() string {
	return fmt.Sprintf("%08x", rand.Uint32())
}

// getTerminationMessage looks on the filesystem for the provided termination message path, returning a limited
// amount of those bytes, or returns true if the logs should be checked.
func getTerminationMessage(status *runtimeapi.ContainerStatus, terminationMessagePath string, fallbackToLogs bool) (string, bool) {
	if len(terminationMessagePath) == 0 {
		return "", fallbackToLogs
	}
	for _, mount := range status.Mounts {
		if mount.ContainerPath != terminationMessagePath {
			continue
		}
		path := mount.HostPath
		data, _, err := tail.ReadAtMost(path, kubecontainer.MaxContainerTerminationMessageLength)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fallbackToLogs
			}
			return fmt.Sprintf("Error on reading termination log %s: %v", path, err), false
		}
		return string(data), (fallbackToLogs && len(data) == 0)
	}
	return "", fallbackToLogs
}
