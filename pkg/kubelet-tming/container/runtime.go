package container

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"time"
	"reflect"
)

type Version interface {
	Compare(other string) (int, error)

	String() string
}

type Image struct {
	ID 		string
	RepoTags	[]string
	RepoDigests	[]string
	Size 		int64
}

type ImageSpec struct {
	Image 		string
}

// ImageStats contains statistics about all the images currently available.
type ImageStats struct {
	// Total amount of storage consumed by existing images.
	TotalStorageBytes uint64
}

type ImageService interface {

	GetImageRef(image ImageSpec) (string, error)

	ListImages() ([]Image, error)

	ImageStats() (*ImageStats, error)
}

type Runtime interface {

	ImageService

	GetPods(all bool) ([]*Pod, error)

	Type() string
	Version() (Version, error)

	GarbageCollect(gcPolicy ContainerGCPolicy, allSourcesReady bool, evictNonDeletedPods bool) error
}

type ContainerState string

const (
	ContainerStateCreated 	ContainerState 	= 	"created"
	ContainerStateRunning 	ContainerState 	= 	"running"
	ContainerStateExited 	ContainerState 	= 	"exited"

	ContainerStateUnknown 	ContainerState 	= 	"unknown"
)


func GetPodFullName(pod *v1.Pod) string {
	return pod.Name + "_" + pod.Namespace
}

// Build the pod full name from pod name and namespace.
func BuildPodFullName(name, namespace string) string {
	return name + "_" + namespace
}

// PodStatus represents the status of the pod and its containers.
// v1.PodStatus can be derived from examining PodStatus and v1.Pod.
type PodStatus struct {
	// ID of the pod.
	ID types.UID
	// Name of the pod.
	Name string
	// Namespace of the pod.
	Namespace string
	// IP of the pod.
	IP string
	// Status of containers in the pod.
	ContainerStatuses []*ContainerStatus
	// Status of the pod sandbox.
	// Only for kuberuntime now, other runtime may keep it nil.
	SandboxStatuses []*runtimeapi.PodSandboxStatus
}

// ContainerStatus represents the status of a container.
type ContainerStatus struct {
	// ID of the container.
	ID ContainerID
	// Name of the container.
	Name string
	// Status of the container.
	State ContainerState
	// Creation time of the container.
	CreatedAt time.Time
	// Start time of the container.
	StartedAt time.Time
	// Finish time of the container.
	FinishedAt time.Time
	// Exit code of the container.
	ExitCode int
	// Name of the image, this also includes the tag of the image,
	// the expected form is "NAME:TAG".
	Image string
	// ID of the image.
	ImageID string
	// Hash of the container, used for comparison.
	Hash uint64
	// Number of times that the container has been restarted.
	RestartCount int
	// A string explains why container is in such a status.
	Reason string
	// Message written by the container before exiting (stored in
	// TerminationMessagePath).
	Message string
}



type ContainerID struct {
	Type 		string
	ID 		string
}

type Container struct {
	ID 		ContainerID
	Name 		string
	Image 		string
	ImageID 	string
	Hash 		uint64
	State 		ContainerState
}

type Pod struct {
	ID 		types.UID
	Name 		string
	Namespace 	string
	Containers 	[]*Container
	Sandboxes 	[]*Container
}


type Pods []*Pod

// FindPodByID finds and returns a pod in the pod list by UID. It will return an empty pod
// if not found.
func (p Pods) FindPodByID(podUID types.UID) Pod {
	for i := range p {
		if p[i].ID == podUID {
			return *p[i]
		}
	}
	return Pod{}
}

// FindPodByFullName finds and returns a pod in the pod list by the full name.
// It will return an empty pod if not found.
func (p Pods) FindPodByFullName(podFullName string) Pod {
	for i := range p {
		if BuildPodFullName(p[i].Name, p[i].Namespace) == podFullName {
			return *p[i]
		}
	}
	return Pod{}
}

// FindPod combines FindPodByID and FindPodByFullName, it finds and returns a pod in the
// pod list either by the full name or the pod ID. It will return an empty pod
// if not found.
func (p Pods) FindPod(podFullName string, podUID types.UID) Pod {
	if len(podFullName) > 0 {
		return p.FindPodByFullName(podFullName)
	}
	return p.FindPodByID(podUID)
}

// FindContainerByName returns a container in the pod with the given name.
// When there are multiple containers with the same name, the first match will
// be returned.
func (p *Pod) FindContainerByName(containerName string) *Container {
	for _, c := range p.Containers {
		if c.Name == containerName {
			return c
		}
	}
	return nil
}

func (p *Pod) FindContainerByID(id ContainerID) *Container {
	for _, c := range p.Containers {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func (p *Pod) FindSandboxByID(id ContainerID) *Container {
	for _, c := range p.Sandboxes {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// ToAPIPod converts Pod to v1.Pod. Note that if a field in v1.Pod has no
// corresponding field in Pod, the field would not be populated.
func (p *Pod) ToAPIPod() *v1.Pod {
	var pod v1.Pod
	pod.UID = p.ID
	pod.Name = p.Name
	pod.Namespace = p.Namespace

	for _, c := range p.Containers {
		var container v1.Container
		container.Name = c.Name
		container.Image = c.Image
		pod.Spec.Containers = append(pod.Spec.Containers, container)
	}
	return &pod
}

// IsEmpty returns true if the pod is empty.
func (p *Pod) IsEmpty() bool {
	return reflect.DeepEqual(p, &Pod{})
}
