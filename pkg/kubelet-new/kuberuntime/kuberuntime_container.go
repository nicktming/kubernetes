package kuberuntime

import (
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	v1 "k8s.io/api/core/v1"
	"strings"
	"fmt"
	"k8s.io/klog"
	"google.golang.org/grpc"
	"k8s.io/kubernetes/pkg/kubelet/events"
	"k8s.io/kubernetes/pkg/kubelet-new/types"
	kubetypes "k8s.io/apimachinery/pkg/types"
	"errors"
	"sort"
	"time"
)

var (
	// ErrCreateContainerConfig - failed to create container config
	ErrCreateContainerConfig = errors.New("CreateContainerConfigError")
	// ErrCreateContainer - failed to create container
	ErrCreateContainer = errors.New("CreateContainerError")
	// ErrPreStartHook - failed to execute PreStartHook
	ErrPreStartHook = errors.New("PreStartHookError")
	// ErrPostStartHook - failed to execute PostStartHook
	ErrPostStartHook = errors.New("PostStartHookError")
)

// getKubeletContainers lists containers managed by kubelet.
// The boolean parameter specifies whether returns all containers including
// those already exited and dead containers (used for garbage collection).
func (m *kubeGenericRuntimeManager) getKubeletContainers(allContainers bool) ([]*runtimeapi.Container, error) {
	filter := &runtimeapi.ContainerFilter{}
	if !allContainers {
		filter.State = &runtimeapi.ContainerStateValue{
			State: runtimeapi.ContainerState_CONTAINER_RUNNING,
		}
	}

	containers, err := m.runtimeService.ListContainers(filter)
	if err != nil {
		klog.Errorf("getKubeletContainers failed: %v", err)
		return nil, err
	}

	return containers, nil
}


// recordContainerEvent should be used by the runtime manager for all container related events.
// it has sanity checks to ensure that we do not write events that can abuse our masters.
// in particular, it ensures that a containerID never appears in an event message as that
// is prone to causing a lot of distinct events that do not count well.
// it replaces any reference to a containerID with the containerName which is stable, and is what users know.
func (m *kubeGenericRuntimeManager) recordContainerEvent(pod *v1.Pod, container *v1.Container, containerID, eventType, reason, message string, args ...interface{}) {
	ref, err := kubecontainer.GenerateContainerRef(pod, container)
	if err != nil {
		klog.Errorf("Can't make a ref to pod %q, container %v: %v", format.Pod(pod), container.Name, err)
		return
	}
	eventMessage := message
	if len(args) > 0 {
		eventMessage = fmt.Sprintf(message, args...)
	}
	// this is a hack, but often the error from the runtime includes the containerID
	// which kills our ability to deduplicate events.  this protection makes a huge
	// difference in the number of unique events
	if containerID != "" {
		eventMessage = strings.Replace(eventMessage, containerID, container.Name, -1)
	}
	m.recorder.Event(ref, eventType, reason, eventMessage)
}


func (m *kubeGenericRuntimeManager) startContainer(podSandboxID string, podSandboxConfig *runtimeapi.PodSandboxConfig, container *v1.Container, pod *v1.Pod, podIP string, podStatus *kubecontainer.PodStatus) (string, error) {
	restartCount := 0
	if podStatus != nil {
		containerStatus := podStatus.FindContainerStatusByName(container.Name)
		if containerStatus != nil {
			restartCount = containerStatus.RestartCount + 1
		}
	}
	imageRef := container.Image
	containerConfig, cleanupAction, err := m.generateContainerConfig(container, pod, restartCount, podIP, imageRef)
	if cleanupAction != nil {
		defer cleanupAction()
	}
	if err != nil {
		m.recordContainerEvent(pod, container, "", v1.EventTypeWarning, events.FailedToCreateContainer, "Error: %v", grpc.ErrorDesc(err))
		return grpc.ErrorDesc(err), ErrCreateContainerConfig
	}
	containerID, err := m.runtimeService.CreateContainer(podSandboxID, containerConfig, podSandboxConfig)
	if err != nil {
		m.recordContainerEvent(pod, container, containerID, v1.EventTypeWarning, events.FailedToCreateContainer, "Error: %v", grpc.ErrorDesc(err))
		return grpc.ErrorDesc(err), ErrCreateContainer
	}
	err = m.runtimeService.StartContainer(containerID)
	if err != nil {
		m.recordContainerEvent(pod, container, containerID, v1.EventTypeWarning, events.FailedToStartContainer, "Error: %v", grpc.ErrorDesc(err))
		return grpc.ErrorDesc(err), kubecontainer.ErrRunContainer
	}
	m.recordContainerEvent(pod, container, containerID, v1.EventTypeNormal, events.StartedContainer, fmt.Sprintf("Started container %s", container.Name))
	return "", nil
}

// generateContainerConfig generates container config for kubelet runtime v1.
func (m *kubeGenericRuntimeManager) generateContainerConfig(container *v1.Container, pod *v1.Pod, restartCount int, podIP, imageRef string) (*runtimeapi.ContainerConfig, func(), error) {
	logDir := BuildContainerLogsDirectory(pod.Namespace, pod.Name, pod.UID, container.Name)
	err := m.osInterface.MkdirAll(logDir, 0755)
	if err != nil {
		// TODO cleanup Action
		return nil, nil, fmt.Errorf("create container log directory for container %s failed: %v", container.Name, err)
	}
	containerLogsPath := buildContainerLogsPath(container.Name, restartCount)
	command := []string{"sleep"}
	args := []string{"100000"}
	restartCountUint32 := uint32(restartCount)
	config := &runtimeapi.ContainerConfig{
		Metadata: &runtimeapi.ContainerMetadata{
			Name:    container.Name,
			Attempt: restartCountUint32,
		},
		Image:       &runtimeapi.ImageSpec{Image: imageRef},
		Command:     command,
		Args:        args,
		WorkingDir:  container.WorkingDir,
		Labels:      newContainerLabels(container, pod),
		//Annotations: newContainerAnnotations(container, pod, restartCount, opts),
		//Devices:     makeDevices(opts),
		//Mounts:      m.makeMounts(opts, container),
		LogPath:     containerLogsPath,
		Stdin:       container.Stdin,
		StdinOnce:   container.StdinOnce,
		Tty:         container.TTY,
	}
	// TODO config
	return config, nil, nil
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
			//annotatedInfo := getContainerInfoFromAnnotations(status.Annotations)
			//fallbackToLogs := annotatedInfo.TerminationMessagePolicy == v1.TerminationMessageFallbackToLogsOnError && cStatus.ExitCode != 0
			//tMessage, checkLogs := getTerminationMessage(status, annotatedInfo.TerminationMessagePath, fallbackToLogs)
			//if checkLogs {
			//	// if dockerLegacyService is populated, we're supposed to use it to fetch logs
			//	if m.legacyLogProvider != nil {
			//		tMessage, err = m.legacyLogProvider.GetContainerLogTail(uid, name, namespace, kubecontainer.ContainerID{Type: m.runtimeName, ID: c.Id})
			//		if err != nil {
			//			tMessage = fmt.Sprintf("Error reading termination message from logs: %v", err)
			//		}
			//	} else {
			//		tMessage = m.readLastStringFromContainerLogs(status.GetLogPath())
			//	}
			//}
			//// Use the termination message written by the application is not empty
			//if len(tMessage) != 0 {
			//	cStatus.Message = tMessage
			//}
		}
		statuses[i] = cStatus
	}

	sort.Sort(containerStatusByCreated(statuses))
	return statuses, nil
}

func toKubeContainerStatus(status *runtimeapi.ContainerStatus, runtimeName string) *kubecontainer.ContainerStatus {
	//annotatedInfo := getContainerInfoFromAnnotations(status.Annotations)
	labeledInfo := getContainerInfoFromLabels(status.Labels)
	cStatus := &kubecontainer.ContainerStatus{
		ID: kubecontainer.ContainerID{
			Type: runtimeName,
			ID:   status.Id,
		},
		Name:         labeledInfo.ContainerName,
		Image:        status.Image.Image,
		ImageID:      status.ImageRef,
		//Hash:         annotatedInfo.Hash,
		//RestartCount: annotatedInfo.RestartCount,
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