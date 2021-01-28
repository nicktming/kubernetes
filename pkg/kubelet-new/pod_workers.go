package kubelet_new

import (
	//"fmt"
	//"strings"
	//"sync"
	"time"

	"k8s.io/api/core/v1"
	//"k8s.io/apimachinery/pkg/types"
	//"k8s.io/apimachinery/pkg/util/runtime"
	//"k8s.io/apimachinery/pkg/util/wait"
	//"k8s.io/client-go/tools/record"
	//"k8s.io/klog"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	//"k8s.io/kubernetes/pkg/kubelet/events"
	//"k8s.io/kubernetes/pkg/kubelet/eviction"
	kubetypes "k8s.io/kubernetes/pkg/kubelet-new/types"
	//"k8s.io/kubernetes/pkg/kubelet/util/format"
	//"k8s.io/kubernetes/pkg/kubelet/util/queue"
)

// OnCompleteFunc is a function that is invoked when an operation completes.
// If err is non-nil, the operation did not complete successfully.
type OnCompleteFunc func(err error)

// PodStatusFunc is a function that is invoked to generate a pod status.
type PodStatusFunc func(pod *v1.Pod, podStatus *kubecontainer.PodStatus) v1.PodStatus

// KillPodOptions are options when performing a pod update whose update type is kill.
type KillPodOptions struct {
	// PodStatusFunc is the function to invoke to set pod status in response to a kill request.
	PodStatusFunc PodStatusFunc
	// PodTerminationGracePeriodSecondsOverride is optional override to use if a pod is being killed as part of kill operation.
	PodTerminationGracePeriodSecondsOverride *int64
}

// UpdatePodOptions is an options struct to pass to a UpdatePod operation.
type UpdatePodOptions struct {
	// pod to update
	Pod *v1.Pod
	// the mirror pod for the pod to update, if it is a static pod
	MirrorPod *v1.Pod
	// the type of update (create, update, sync, kill)
	UpdateType kubetypes.SyncPodType
	// optional callback function when operation completes
	// this callback is not guaranteed to be completed since a pod worker may
	// drop update requests if it was fulfilling a previous request.  this is
	// only guaranteed to be invoked in response to a kill pod request which is
	// always delivered.
	OnCompleteFunc OnCompleteFunc
	// if update type is kill, use the specified options to kill the pod.
	KillPodOptions *KillPodOptions
}

// syncPodOptions provides the arguments to a SyncPod operation.
type syncPodOptions struct {
	// the mirror pod for the pod to sync, if it is a static pod
	mirrorPod *v1.Pod
	// pod to sync
	pod *v1.Pod
	// the type of update (create, update, sync)
	updateType kubetypes.SyncPodType
	// the current status
	podStatus *kubecontainer.PodStatus
	// if update type is kill, use the specified options to kill the pod.
	killPodOptions *KillPodOptions
}

// the function to invoke to perform a sync.
type syncPodFnType func(options syncPodOptions) error

const (
	// jitter factor for resyncInterval
	workerResyncIntervalJitterFactor = 0.5

	// jitter factor for backOffPeriod and backOffOnTransientErrorPeriod
	workerBackOffPeriodJitterFactor = 0.5

	// backoff period when transient error occurred.
	backOffOnTransientErrorPeriod = time.Second
)


