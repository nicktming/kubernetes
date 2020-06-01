package container

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"time"
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

type ImageService interface {

	GetImageRef(image ImageSpec) (string, error)

	ListImages() ([]Image, error)
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