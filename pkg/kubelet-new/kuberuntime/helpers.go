package kuberuntime

import (
	"strings"
	"path/filepath"
	"k8s.io/apimachinery/pkg/types"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	"fmt"
)

// logPathDelimiter is the delimiter used in the log path.
const logPathDelimiter = "_"

// BuildPodLogsDirectory builds absolute log directory path for a pod sandbox.
func BuildPodLogsDirectory(podNamespace, podName string, podUID types.UID) string {
	return filepath.Join(podLogsRootDirectory, strings.Join([]string{podNamespace, podName,
		string(podUID)}, logPathDelimiter))
}

// BuildContainerLogsDirectory builds absolute log directory path for a container in pod.
func BuildContainerLogsDirectory(podNamespace, podName string, podUID types.UID, containerName string) string {
	return filepath.Join(BuildPodLogsDirectory(podNamespace, podName, podUID), containerName)
}

// buildContainerLogsPath builds log path for container relative to pod logs directory.
func buildContainerLogsPath(containerName string, restartCount int) string {
	return filepath.Join(containerName, fmt.Sprintf("%d.log", restartCount))
}

// sandboxToKubeContainer converts runtimeapi.PodSandbox to kubecontainer.Container.
// This is only needed because we need to return sandboxes as if they were
// kubecontainer.Containers to avoid substantial changes to PLEG.
// TODO: Remove this once it becomes obsolete.
func (m *kubeGenericRuntimeManager) sandboxToKubeContainer(s *runtimeapi.PodSandbox) (*kubecontainer.Container, error) {
	if s == nil || s.Id == "" {
		return nil, fmt.Errorf("unable to convert a nil pointer to a runtime container")
	}

	return &kubecontainer.Container{
		ID:    kubecontainer.ContainerID{Type: m.runtimeName, ID: s.Id},
		State: kubecontainer.SandboxToContainerState(s.State),
	}, nil
}

func toKubeConainerState(state runtimeapi.ContainerState) kubecontainer.ContainerState {
	switch state {
	case runtimeapi.ContainerState_CONTAINER_CREATED:
		return kubecontainer.ContainerStateCreated
	case runtimeapi.ContainerState_CONTAINER_RUNNING:
		return kubecontainer.ContainerStateRunning
	case runtimeapi.ContainerState_CONTAINER_EXITED:
		return kubecontainer.ContainerStateExited
	case runtimeapi.ContainerState_CONTAINER_UNKNOWN:
		return kubecontainer.ContainerStateUnknown
	}
	return kubecontainer.ContainerStateUnknown
}

func (m *kubeGenericRuntimeManager) toKubeContainer(c *runtimeapi.Container) (*kubecontainer.Container, error) {
	if c == nil || c.Id == "" || c.Image == nil {
		return nil, fmt.Errorf("unable to convert a nil pointer to a runtime container")
	}
	return &kubecontainer.Container{
		ID: 		kubecontainer.ContainerID{Type: m.runtimeName, ID: c.Id},
		Name: 		c.Metadata.Name,
		ImageID: 	c.ImageRef,
		Image:		c.Image.Image,
		// TODO HASH
		State:		toKubeConainerState(c.State),
	}, nil
}

