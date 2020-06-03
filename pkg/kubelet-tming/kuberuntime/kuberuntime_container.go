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

	"google.golang.org/grpc"
	"encoding/json"
	"errors"
	"path/filepath"
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


// startContainer starts a container and returns a message indicates why it is failed on error.
// It starts the container through the following steps:
// * pull the image
// * create the container
// * start the container
// * run the post start lifecycle hooks (if applicable)

func (m *kubeGenericRuntimeManager) startContainer(podSandboxID string, podSandboxConfig *runtimeapi.PodSandboxConfig, container *v1.Container, pod *v1.Pod, podStatus *kubecontainer.PodStatus, pullSecrets []v1.Secret, podIP string) (string, error) {
	// Step 1: pull the image.
	imageRef, msg, err := m.imagePuller.EnsureImageExists(pod, container, pullSecrets, podSandboxConfig)
	if err != nil {
		//m.recordContainerEvent(pod, container, "", v1.EventTypeWarning, events.FailedToCreateContainer, "Error: %v", grpc.ErrorDesc(err))
		return msg, err
	}

	// Step 2: create the container
	ref, err := kubecontainer.GenerateContainerRef(pod, container)
	if err != nil {
		klog.Errorf("Can't make a ref to pod %q, container %v: %v", format.Pod(pod), container.Name, err)
	}
	prettyRef, _ := json.MarshalIndent(ref, "", "\t")
	klog.Infof("Generating ref for container %s: %v", container.Name, string(prettyRef))

	restartCount := 0
	containerStatus := podStatus.FindContainerStatusByName(container.Name)
	if containerStatus != nil {
		restartCount = containerStatus.RestartCount + 1
	}

	containerConfig, cleanupAction, err := m.generateContainerConfig(container, pod, restartCount, podIP, imageRef)
	if cleanupAction != nil {
		defer cleanupAction()
	}
	if err != nil {
		//m.recordContainerEvent(pod, container, "", v1.EventTypeWarning, events.FailedToCreateContainer, "Error: %v", grpc.ErrorDesc(err))
		return grpc.ErrorDesc(err), ErrCreateContainerConfig
	}

	containerID, err := m.runtimeService.CreateContainer(podSandboxID, containerConfig, podSandboxConfig)
	if err != nil {
		//m.recordContainerEvent(pod, container, containerID, v1.EventTypeWarning, events.FailedToCreateContainer, "Error: %v", grpc.ErrorDesc(err))
		return grpc.ErrorDesc(err), ErrCreateContainer
	}

	//err = m.internalLifecycle.PreStartContainer(pod, container, containerID)
	//if err != nil {
	//	m.recordContainerEvent(pod, container, containerID, v1.EventTypeWarning, events.FailedToStartContainer, "Internal PreStartContainer hook failed: %v", grpc.ErrorDesc(err))
	//	return grpc.ErrorDesc(err), ErrPreStartHook
	//}
	//m.recordContainerEvent(pod, container, containerID, v1.EventTypeNormal, events.CreatedContainer, fmt.Sprintf("Created container %s", container.Name))
	//
	//if ref != nil {
	//	m.containerRefManager.SetRef(kubecontainer.ContainerID{
	//		Type: m.runtimeName,
	//		ID:   containerID,
	//	}, ref)
	//}

	err = m.runtimeService.StartContainer(containerID)
	if err != nil {
		//m.recordContainerEvent(pod, container, containerID, v1.EventTypeWarning, events.FailedToStartContainer, "Error: %v", grpc.ErrorDesc(err))
		return grpc.ErrorDesc(err), kubecontainer.ErrRunContainer
	}
	//m.recordContainerEvent(pod, container, containerID, v1.EventTypeNormal, events.StartedContainer, fmt.Sprintf("Started container %s", container.Name))
	containerMeta := containerConfig.GetMetadata()
	sandboxMeta := podSandboxConfig.GetMetadata()
	legacySymlink := legacyLogSymlink(containerID, containerMeta.Name, sandboxMeta.Name,
		sandboxMeta.Namespace)
	containerLog := filepath.Join(podSandboxConfig.LogDirectory, containerConfig.LogPath)
	// only create legacy symlink if containerLog path exists (or the error is not IsNotExist).
	// Because if containerLog path does not exist, only dandling legacySymlink is created.
	// This dangling legacySymlink is later removed by container gc, so it does not make sense
	// to create it in the first place. it happens when journald logging driver is used with docker.
	if _, err := m.osInterface.Stat(containerLog); !os.IsNotExist(err) {
		if err := m.osInterface.Symlink(containerLog, legacySymlink); err != nil {
			klog.Errorf("Failed to create legacy symbolic link %q to container %q log %q: %v",
				legacySymlink, containerID, containerLog, err)
		}
	}

	// Step 4: execute the post start hook.
	//if container.Lifecycle != nil && container.Lifecycle.PostStart != nil {
	//	kubeContainerID := kubecontainer.ContainerID{
	//		Type: m.runtimeName,
	//		ID:   containerID,
	//	}
	//	msg, handlerErr := m.runner.Run(kubeContainerID, pod, container, container.Lifecycle.PostStart)
	//	if handlerErr != nil {
	//		m.recordContainerEvent(pod, container, kubeContainerID.ID, v1.EventTypeWarning, events.FailedPostStartHook, msg)
	//		if err := m.killContainer(pod, kubeContainerID, container.Name, "FailedPostStartHook", nil); err != nil {
	//			klog.Errorf("Failed to kill container %q(id=%q) in pod %q: %v, %v",
	//				container.Name, kubeContainerID.String(), format.Pod(pod), ErrPostStartHook, err)
	//		}
	//		return msg, fmt.Errorf("%s: %v", ErrPostStartHook, handlerErr)
	//	}
	//}

	return "", nil
}




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

	if err := m.osInterface.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove container %q log %q: %v", containerID, path, err)
	}

	// Remove the legacy container log symlink.
	// TODO(random-liu): Remove this after cluster logging supports CRI container log path.
	legacySymlink := legacyLogSymlink(containerID, labeledInfo.ContainerName, labeledInfo.PodName,
		labeledInfo.PodNamespace)
	if err := m.osInterface.Remove(legacySymlink); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove container %q log legacy symbolic link %q: %v",
			containerID, legacySymlink, err)
	}
	return nil
}

// generateContainerConfig generates container config for kubelet runtime v1.
func (m *kubeGenericRuntimeManager) generateContainerConfig(container *v1.Container, pod *v1.Pod, restartCount int, podIP, imageRef string) (*runtimeapi.ContainerConfig, func(), error) {
	// TODO runtimeHelper

	var cleanupAction *runtimeapi.ContainerConfig  = nil
	//opts, cleanupAction, err := m.runtimeHelper.GenerateRunContainerOptions(pod, container, podIP)
	//if err != nil {
	//	return nil, nil, err
	//}

	//uid, username, err := m.getImageUser(container.Image)
	//if err != nil {
	//	return nil, cleanupAction, err
	//}

	// Verify RunAsNonRoot. Non-root verification only supports numeric user.
	//if err := verifyRunAsNonRoot(pod, container, uid, username); err != nil {
	//	return nil, cleanupAction, err
	//}

	envs := make([]kubecontainer.EnvVar, 0)

	command, args := kubecontainer.ExpandContainerCommandAndArgs(container, envs)
	logDir := BuildContainerLogsDirectory(pod.Namespace, pod.Name, pod.UID, container.Name)
	err := m.osInterface.MkdirAll(logDir, 0755)
	if err != nil {
		return nil, cleanupAction, fmt.Errorf("create container log directory for container %s failed: %v", container.Name, err)
	}
	containerLogsPath := buildContainerLogsPath(container.Name, restartCount)
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

	// set platform specific configurations.
	//if err := m.applyPlatformSpecificContainerConfig(config, container, pod, uid, username); err != nil {
	//	return nil, cleanupAction, err
	//}

	// set environment variables
	//envs := make([]*runtimeapi.KeyValue, len(opts.Envs))
	//for idx := range opts.Envs {
	//	e := opts.Envs[idx]
	//	envs[idx] = &runtimeapi.KeyValue{
	//		Key:   e.Name,
	//		Value: e.Value,
	//	}
	//}
	//config.Envs = envs

	return config, cleanupAction, nil
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

		go func (container *kubecontainer.Container) {
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
