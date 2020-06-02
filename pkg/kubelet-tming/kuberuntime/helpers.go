package kuberuntime

import (
	"fmt"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)


type podsByID []*kubecontainer.Pod

func (b podsByID) Len() int           { return len(b) }
func (b podsByID) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }
func (b podsByID) Less(i, j int) bool { return b[i].ID < b[j].ID }

type containersByID []*kubecontainer.Container

func (b containersByID) Len() int           { return len(b) }
func (b containersByID) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }
func (b containersByID) Less(i, j int) bool { return b[i].ID.ID < b[j].ID.ID }

// Newest first.
type podSandboxByCreated []*runtimeapi.PodSandbox

func (p podSandboxByCreated) Len() int           { return len(p) }
func (p podSandboxByCreated) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p podSandboxByCreated) Less(i, j int) bool { return p[i].CreatedAt > p[j].CreatedAt }

type containerStatusByCreated []*kubecontainer.ContainerStatus

func (c containerStatusByCreated) Len() int           { return len(c) }
func (c containerStatusByCreated) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }
func (c containerStatusByCreated) Less(i, j int) bool { return c[i].CreatedAt.After(c[j].CreatedAt) }




func (m *kubeGenericRuntimeManager) sandboxToKubeContainer(s *runtimeapi.PodSandbox) (*kubecontainer.Container, error) {
	if s == nil || s.Id == "" {
		return nil, fmt.Errorf("unable to convert a nil pointer to a runtime container")
	}

	return &kubecontainer.Container{
		ID:    kubecontainer.ContainerID{Type: m.runtimeName, ID: s.Id},
		State: kubecontainer.SandboxToContainerState(s.State),
	}, nil
}



func (m *kubeGenericRuntimeManager) toKubeContainer(c *runtimeapi.Container) (*kubecontainer.Container, error) {
	if c == nil || c.Id == "" || c.Image == nil {
		return nil, fmt.Errorf("unable to convert a nil pointer to a runtime container")
	}

	annotatedInfo := getContainerInfoFromAnnotations(c.Annotations)
	return &kubecontainer.Container{
		ID:      kubecontainer.ContainerID{Type: m.runtimeName, ID: c.Id},
		Name:    c.GetMetadata().GetName(),
		ImageID: c.ImageRef,
		Image:   c.Image.Image,
		Hash:    annotatedInfo.Hash,
		State:   toKubeContainerState(c.State),
	}, nil
}

func toKubeContainerState(state runtimeapi.ContainerState) kubecontainer.ContainerState {
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