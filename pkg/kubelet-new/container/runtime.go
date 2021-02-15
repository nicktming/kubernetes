package container


import (
	"k8s.io/api/core/v1"
	//"k8s.io/client-go/util/flowcontrol"
	"k8s.io/apimachinery/pkg/types"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog"
	"fmt"
	"strings"
	"time"
)

type Runtime interface {
	// Type returns the type of the container runtime.
	Type() string

	// GetPodStatus retrieves the status of the pod, including the
	// information of all containers in the pod that are visible in Runtime.
	GetPodStatus(uid types.UID, name, namespace string) (*PodStatus, error)

	// TODO podStatus
	// Syncs the running pod into the desired pod.
	SyncPod(pod *v1.Pod, podStatus *PodStatus) PodSyncResult

	// KillPod kills all the containers of a pod. Pod may be nil, running pod must not be.
	// TODO(random-liu): Return PodSyncResult in KillPod.
	// gracePeriodOverride if specified allows the caller to override the pod default grace period.
	// only hard kill paths are allowed to specify a gracePeriodOverride in the kubelet in order to not corrupt user data.
	// it is useful when doing SIGKILL for hard eviction scenarios, or max grace period during soft eviction scenarios.
	KillPod(pod *v1.Pod, runningPod Pod, gracePeriodOverride *int64) error

	// Delete a container. If the container is still running, an error is returned.
	DeleteContainer(containerID ContainerID) error


	GetPods(all bool) ([]*Pod, error)


	// GarbageCollect removes dead containers using the specified container gc policy
	// If allSourcesReady is not true, it means that kubelet doesn't have the
	// complete list of pods from all avialble sources (e.g., apiserver, http,
	// file). In this case, garbage collector should refrain itself from aggressive
	// behavior such as removing all containers of unrecognized pods (yet).
	// If evictNonDeletedPods is set to true, containers and sandboxes belonging to pods
	// that are terminated, but not deleted will be evicted.  Otherwise, only deleted pods will be GC'd.
	// TODO: Revisit this method and make it cleaner.
	// TODO gcPolicy ContainerGCPolicy allSourcesReady bool
	GarbageCollect(evictNonDeletedPods bool) error
}

type PodStatus struct {
	// ID of the pod.
	ID types.UID
	// Name of the pod
	Name string
	// Namespace of the pod
	Namespace string
	// IP of the pod
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

// Pod is a group of containers.
type Pod struct {
	// The ID of the pod, which can be used to retrieve a particular pod
	// from the pod list returned by GetPods().
	ID types.UID
	// The name and namespace of the pod, which is readable by human.
	Name      string
	Namespace string
	// List of containers that belongs to this pod. It may contain only
	// running containers, or mixed with dead ones (when GetPods(true)).
	Containers []*Container
	// List of sandboxes associated with this pod. The sandboxes are converted
	// to Container temporariliy to avoid substantial changes to other
	// components. This is only populated by kuberuntime.
	// TODO: use the runtimeApi.PodSandbox type directly.
	Sandboxes []*Container
}

// ContainerID is a type that identifies a container.
type ContainerID struct {
	// The type of the container runtime. e.g. 'docker'.
	Type string
	// The identification of the container, this is comsumable by
	// the underlying container runtime. (Note that the container
	// runtime interface still takes the whole struct as input).
	ID string
}

type ContainerCommandRunner interface {
	// RunInContainer synchronously executes the command in the container, and returns the output.
	// If the command completes with a non-0 exit code, a k8s.io/utils/exec.ExitError will be returned.
	RunInContainer(id ContainerID, cmd []string, timeout time.Duration) ([]byte, error)
}


func BuildContainerID(typ, ID string) ContainerID {
	return ContainerID{Type: typ, ID: ID}
}

// Convenience method for creating a ContainerID from an ID string.
func ParseContainerID(containerID string) ContainerID {
	var id ContainerID
	if err := id.ParseString(containerID); err != nil {
		klog.Error(err)
	}
	return id
}

func (c *ContainerID) ParseString(data string) error {
	// Trim the quotes and split the type and ID.
	parts := strings.Split(strings.Trim(data, "\""), "://")
	if len(parts) != 2 {
		return fmt.Errorf("invalid container ID: %q", data)
	}
	c.Type, c.ID = parts[0], parts[1]
	return nil
}

func (c *ContainerID) String() string {
	return fmt.Sprintf("%s://%s", c.Type, c.ID)
}

func (c *ContainerID) IsEmpty() bool {
	return *c == ContainerID{}
}

func (c *ContainerID) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%q", c.String())), nil
}

func (c *ContainerID) UnmarshalJSON(data []byte) error {
	return c.ParseString(string(data))
}

// DockerID is an ID of docker container. It is a type to make it clear when we're working with docker container Ids
type DockerID string

func (id DockerID) ContainerID() ContainerID {
	return ContainerID{
		Type: "docker",
		ID:   string(id),
	}
}


type ContainerState string

const (
	ContainerStateCreated ContainerState = "created"
	ContainerStateRunning ContainerState = "running"
	ContainerStateExited  ContainerState = "exited"
	// This unknown encompasses all the states that we currently don't care.
	ContainerStateUnknown ContainerState = "unknown"
)

// Container provides the runtime information for a container, such as ID, hash,
// state of the container.
type Container struct {
	// The ID of the container, used by the container runtime to identify
	// a container.
	ID ContainerID
	// The name of the container, which should be the same as specified by
	// v1.Container.
	Name string
	// The image name of the container, this also includes the tag of the image,
	// the expected form is "NAME:TAG".
	Image string
	// The id of the image used by the container.
	ImageID string
	// Hash of the container, used for comparison. Optional for containers
	// not managed by kubelet.
	Hash uint64
	// State is the state of the container.
	State ContainerState
}


func (ps *PodStatus) GetContainerStatusFromPodStatus(name string) *ContainerStatus {
	for _, cs := range ps.ContainerStatuses {
		if cs.Name == name {
			return cs
		}
	}
	return nil
}

func (ps *PodStatus) FindContainerStatusByName(name string) *ContainerStatus {
	for _, cs := range ps.ContainerStatuses {
		if cs.Name == name {
			return cs
		}
	}
	return nil
}


const (
	// MaxPodTerminationMessageLogLength is the maximum bytes any one pod may have written
	// as termination message output across all containers. Containers will be evenly truncated
	// until output is below this limit.
	MaxPodTerminationMessageLogLength = 1024 * 12
	// MaxContainerTerminationMessageLength is the upper bound any one container may write to
	// its termination message path. Contents above this length will be truncated.
	MaxContainerTerminationMessageLength = 1024 * 4
	// MaxContainerTerminationMessageLogLength is the maximum bytes any one container will
	// have written to its termination message when the message is read from the logs.
	MaxContainerTerminationMessageLogLength = 1024 * 2
	// MaxContainerTerminationMessageLogLines is the maximum number of previous lines of
	// log output that the termination message can contain.
	MaxContainerTerminationMessageLogLines = 80
)



