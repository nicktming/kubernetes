package kubelet_new

import (
	//"fmt"
	"strings"
	//"sync"
	"time"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	//"k8s.io/kubernetes/pkg/kubelet/events"
	//"k8s.io/kubernetes/pkg/kubelet/eviction"
	kubetypes "k8s.io/kubernetes/pkg/kubelet-new/types"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	//"k8s.io/kubernetes/pkg/kubelet/util/queue"
	"sync"
	"k8s.io/kubernetes/pkg/kubelet/util/queue"
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

// PodWorkers is an abstract interface for testability.
type PodWorkers interface {
	UpdatePod(options *UpdatePodOptions)
	//ForgetNonExistingPodWorkers(desiredPods map[types.UID]empty)
	//ForgetWorker(uid types.UID)
}


type podWorkers struct {
	// Protects all per worker fields.
	podLock sync.Mutex

	// Tracks all running per-pod goroutines - per-pod goroutine will be
	// processing updates received through its corresponding channel.
	podUpdates map[types.UID]chan UpdatePodOptions

	// podCache stores kubecontainer.PodStatus for all pods.
	podCache kubecontainer.Cache

	// Track the current state of per-pod goroutines.
	// Currently all update request for a given pod coming when another
	// update of this pod is being processed are ignored.
	isWorking map[types.UID]bool
	// Tracks the last undelivered work item for this pod - a work item is
	// undelivered if it comes in while the worker is working.
	lastUndeliveredWorkUpdate map[types.UID]UpdatePodOptions

	workQueue queue.WorkQueue

	// This function is run to sync the desired stated of pod.
	// NOTE: This function has to be thread-safe - it can be called for
	// different pods at the sa

	syncPodFn syncPodFnType

	// backOffPeriod is the duration to back off when there is a sync error.
	backOffPeriod time.Duration

	// resyncInterval is the duration to wait until the next sync.
	resyncInterval time.Duration


	// The EventRecorder to use
	recorder record.EventRecorder
}


func newPodWorkers(syncPodFn syncPodFnType, recorder record.EventRecorder, workQueue queue.WorkQueue,
resyncInterval, backOffPeriod time.Duration, podCache kubecontainer.Cache) *podWorkers {
	return &podWorkers{
		podUpdates:                map[types.UID]chan UpdatePodOptions{},
		isWorking:                 map[types.UID]bool{},
		lastUndeliveredWorkUpdate: map[types.UID]UpdatePodOptions{},
		syncPodFn:                 syncPodFn,
		recorder:                  recorder,
		workQueue:                 workQueue,
		resyncInterval:            resyncInterval,
		backOffPeriod:             backOffPeriod,
		podCache:                  podCache,
	}
}


func (p *podWorkers) wrapUp(uid types.UID, syncErr error) {
	// Requeue the last update if the last sync returned error.
	switch {
	case syncErr == nil:
		// No error; requeue at the regular resync interval.
		p.workQueue.Enqueue(uid, wait.Jitter(p.resyncInterval, workerResyncIntervalJitterFactor))
	case strings.Contains(syncErr.Error(), NetworkNotReadyErrorMsg):
		// Network is not ready; back off for short period of time and retry as network might be ready soon.
		p.workQueue.Enqueue(uid, wait.Jitter(backOffOnTransientErrorPeriod, workerBackOffPeriodJitterFactor))
	default:
		// Error occurred during the sync; back off and then retry.
		p.workQueue.Enqueue(uid, wait.Jitter(p.backOffPeriod, workerBackOffPeriodJitterFactor))
	}
	p.checkForUpdates(uid)
}

func (p *podWorkers) checkForUpdates(uid types.UID) {
	p.podLock.Lock()
	defer p.podLock.Unlock()
	if workUpdate, exists := p.lastUndeliveredWorkUpdate[uid]; exists {
		p.podUpdates[uid] <- workUpdate
		delete(p.lastUndeliveredWorkUpdate, uid)
	} else {
		p.isWorking[uid] = false
	}
}

func (p *podWorkers) managePodLoop(podUpdates <-chan UpdatePodOptions) {
	var lastSyncTime time.Time
	for update := range podUpdates {
		err := func() error {
			podUID := update.Pod.UID
			// This is a blocking call that would return only if the cache
			// has an entry for the pod that is newer than minRuntimeCache
			// Time. This ensures the worker doesn't start syncing until
			// after the cache is at least newer than the finished time of
			// the previous sync.
			//status, err := p.podCache.GetNewerThan(podUID, lastSyncTime)
			status, err := p.podCache.Get(podUID)
			if err != nil {
				// This is the legacy event thrown by manage pod loop
				// all other events are now dispatched from syncPodFn
				//p.recorder.Eventf(update.Pod, v1.EventTypeWarning, events.FailedSync, "error determining status: %v", err)
				return err
			}
			err = p.syncPodFn(syncPodOptions{
				mirrorPod:      update.MirrorPod,
				pod:            update.Pod,
				podStatus:      status,
				killPodOptions: update.KillPodOptions,
				updateType:     update.UpdateType,
			})
			lastSyncTime = time.Now()
			return err
		}()
		// notify the call-back function if the operation succeeded or not
		if update.OnCompleteFunc != nil {
			update.OnCompleteFunc(err)
		}
		if err != nil {
			// IMPORTANT: we do not log errors here, the syncPodFn is responsible for logging errors
			klog.Errorf("Error syncing pod %s (%q), skipping: %v", update.Pod.UID, format.Pod(update.Pod), err)
		}
		p.wrapUp(update.Pod.UID, err)
	}
}

// Apply the new setting to the specified pod.
// If the options provide an OnCompleteFunc, the function is invoked if the update is accepted.
// Update requests are ignored if a kill pod request is pending.
func (p *podWorkers) UpdatePod(options *UpdatePodOptions) {
	pod := options.Pod
	uid := pod.UID
	var podUpdates chan UpdatePodOptions
	var exists bool

	p.podLock.Lock()
	defer p.podLock.Unlock()

	if podUpdates, exists = p.podUpdates[uid]; !exists {
		podUpdates = make(chan UpdatePodOptions, 1)
		p.podUpdates[uid] = podUpdates

		go func() {
			defer runtime.HandleCrash()
			p.managePodLoop(podUpdates)
		}()
	}
	if !p.isWorking[pod.UID] {
		p.isWorking[pod.UID] = true
		podUpdates <- *options
	} else {
		// if a request to kill a pod is pending, we do not let anything overwrite that request.
		update, found := p.lastUndeliveredWorkUpdate[pod.UID]
		if !found || update.UpdateType != kubetypes.SyncPodKill {
			p.lastUndeliveredWorkUpdate[pod.UID] = *options
		}
	}
}




