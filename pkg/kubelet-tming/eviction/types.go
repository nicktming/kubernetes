package eviction


import (
	"time"

	"k8s.io/api/core/v1"
	statsapi "k8s.io/kubernetes/pkg/kubelet/apis/stats/v1alpha1"
	evictionapi "k8s.io/kubernetes/pkg/kubelet-tming/eviction/api"
)

type Manager interface {

	// Start starts the control loop to monitor eviction thresholds at specified interval.
	Start(diskInfoProvider DiskInfoProvider, podFunc ActivePodsFunc, podCleanedUpFunc PodCleanedUpFunc, monitoringInterval time.Duration)

	// IsUnderMemoryPressure returns true if the node is under memory pressure.
	IsUnderMemoryPressure() bool

	// IsUnderDiskPressure returns true if the node is under disk pressure.
	IsUnderDiskPressure() bool

	// IsUnderPIDPressure returns true if the node is under PID pressure.
	IsUnderPIDPressure() bool
}

// Config holds information about how eviction is configured.
type Config struct {
	// PressureTransitionPeriod is duration the kubelet has to wait before transititioning out of a pressure condition.
	PressureTransitionPeriod time.Duration
	// Maximum allowed grace period (in seconds) to use when terminating pods in response to a soft eviction threshold being met.
	MaxPodGracePeriodSeconds int64
	// Thresholds define the set of conditions monitored to trigger eviction.
	Thresholds []evictionapi.Threshold
	// KernelMemcgNotification if true will integrate with the kernel memcg notification to determine if memory thresholds are crossed.
	KernelMemcgNotification bool
	// PodCgroupRoot is the cgroup which contains all pods.
	PodCgroupRoot string
}


// DiskInfoProvider is responsible for informing the manager how disk is configured.
type DiskInfoProvider interface {
	// HasDedicatedImageFs returns true if the imagefs is on a separate device from the rootfs.
	HasDedicatedImageFs() (bool, error)
}


// KillPodFunc kills a pod.
// The pod status is updated, and then it is killed with the specified grace period.
// This function must block until either the pod is killed or an error is encountered.
// Arguments:
// pod - the pod to kill
// status - the desired status to associate with the pod (i.e. why its killed)
// gracePeriodOverride - the grace period override to use instead of what is on the pod spec
type KillPodFunc func(pod *v1.Pod, status v1.PodStatus, gracePeriodOverride *int64) error

// MirrorPodFunc returns the mirror pod for the given static pod and
// whether it was known to the pod manager.
type MirrorPodFunc func(*v1.Pod) (*v1.Pod, bool)

// ActivePodsFunc returns pods bound to the kubelet that are active (i.e. non-terminal state)
type ActivePodsFunc func() []*v1.Pod

// PodCleanedUpFunc returns true if all resources associated with a pod have been reclaimed.
type PodCleanedUpFunc func(*v1.Pod) bool

// statsFunc returns the usage stats if known for an input pod.
type statsFunc func(pod *v1.Pod) (statsapi.PodStats, bool)

// rankFunc sorts the pods in eviction order
type rankFunc func(pods []*v1.Pod, stats statsFunc)
