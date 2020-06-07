package status

import (
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"time"
	clientset "k8s.io/client-go/kubernetes"
	kubepod "k8s.io/kubernetes/pkg/kubelet-tming/pod"
	kubetypes "k8s.io/kubernetes/pkg/kubelet-tming/types"
	"sync"
	"k8s.io/klog"
	"k8s.io/apimachinery/pkg/util/wait"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	"encoding/json"
	statusutil "k8s.io/kubernetes/pkg/util/pod"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"sort"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
)

type versionedPodStatus struct {
	status 		v1.PodStatus

	version 	uint64

	podName 	string
	podNamespace 	string
}

type podStatusSyncRequest struct {
	podUID 		types.UID
	status 		versionedPodStatus
}


// PodStatusProvider knows how to provide status for a pod. It's intended to be used by other components
// that need to introspect status.
type PodStatusProvider interface {
	// GetPodStatus returns the cached status for the provided pod UID, as well as whether it
	// was a cache hit.
	GetPodStatus(uid types.UID) (v1.PodStatus, bool)
}

type PodDeletionSafetyProvider interface {
	// A function which returns true if the pod can safely be deleted
	PodResourcesAreReclaimed(pod *v1.Pod, status v1.PodStatus) bool
}

type manager struct {
	kubeClient 		clientset.Interface
	podManager 		kubepod.Manager

	// Map from pod UID to sync status of the corresponding pod
	podStatuses 		map[types.UID]versionedPodStatus
	podStatusLock		sync.RWMutex
	podStatusChannel	chan podStatusSyncRequest

	apiStatusVersions 	map[kubetypes.MirrorPodUID]uint64
	//podDeletionSafety 	PodDeletionSafetyProvider
}


type Manager interface {
	PodStatusProvider

	// Start the API server status sync loop.
	Start()

	// SetPodStatus caches updates the cached status for the given pod, and triggers a status update.
	SetPodStatus(pod *v1.Pod, status v1.PodStatus)
}


const syncPeriod = 10 * time.Second

func NewManager(kubeClient clientset.Interface, podManager kubepod.Manager,
		//podDeletionSafety PodDeletionSafetyProvider,
		) Manager {
	return &manager {
		kubeClient: 		kubeClient,
		podManager: 		podManager,
		podStatuses:		make(map[types.UID]versionedPodStatus),
		podStatusChannel: 	make(chan podStatusSyncRequest, 1000),
		apiStatusVersions: 	make(map[kubetypes.MirrorPodUID]uint64),
	}
}

func (m *manager) Start() {
	if m.kubeClient == nil {
		klog.Infof("Kubernetes client is nil, not starting status manager.")
		return
	}

	klog.Infof("Starting to sync pod status with apiserver")

	//syncTicker := time.Tick(syncPeriod)
	// syncPod and syncBatch share the same go routine to avoid sync races

	go wait.Forever(func(){
		select {
		case syncRequest := <-m.podStatusChannel:
			klog.Infof("Status Manager: syncing pod: %q, with status: (%d, %v) from podStatusChannel",
				syncRequest.podUID, syncRequest.status.version, syncRequest.status.status)
			m.syncPod(syncRequest.podUID, syncRequest.status)
		}
		//case <-syncTicker:
		//m.syncBatch()
	}, 0)
}

// syncPod syncs the given status with the api server. The caller must not hold the lock.
func (m *manager) syncPod(uid types.UID, status versionedPodStatus) {
	pod, err := m.kubeClient.CoreV1().Pods(status.podNamespace).Get(status.podName, metav1.GetOptions{})

	if errors.IsNotFound(err) {
		klog.Infof("Pod %q (%s) does not exist on the server", status.podName, uid)
		// If the Pod is deleted the status will be cleared in
		// RemoveOrphanedStatuses, so we just ignore the update here.
		return
	}
	if err != nil {
		klog.Warningf("Failed to get status for pod %q: %v", format.PodDesc(status.podName, status.podNamespace, uid), err)
		return
	}

	oldStatus := pod.Status.DeepCopy()
	newPod, patchBytes, err := statusutil.PatchPodStatus(m.kubeClient, pod.Namespace, pod.Name, *oldStatus, mergePodStatus(*oldStatus, status.status))
	klog.Infof("Patch status for pod %q with %q", format.Pod(pod), patchBytes)
	if err != nil {
		klog.Warningf("Failed to update status for pod %q: %v", format.Pod(pod), err)
		return
	}
	pod = newPod
	klog.Infof("Status for pod %q updated successfully: (%d, %+v)", format.Pod(pod), status.version, status.status)

	m.apiStatusVersions[kubetypes.MirrorPodUID(pod.UID)] = status.version


	// We don't handle graceful deletion of mirror pods.
	//if m.canBeDeleted(pod, status.status) {
	//	deleteOptions := metav1.NewDeleteOptions(0)
	//	// Use the pod UID as the precondition for deletion to prevent deleting a newly created pod with the same name and namespace.
	//	deleteOptions.Preconditions = metav1.NewUIDPreconditions(string(pod.UID))
	//	err = m.kubeClient.CoreV1().Pods(pod.Namespace).Delete(pod.Name, deleteOptions)
	//	if err != nil {
	//		klog.Warningf("Failed to delete status for pod %q: %v", format.Pod(pod), err)
	//		return
	//	}
	//	klog.V(3).Infof("Pod %q fully terminated and removed from etcd", format.Pod(pod))
	//	m.deletePodStatus(uid)
	//}
}

func (m *manager) GetPodStatus(uid types.UID) (v1.PodStatus, bool) {
	m.podStatusLock.RLock()
	defer m.podStatusLock.RUnlock()

	status, ok := m.podStatuses[types.UID(m.podManager.TranslatePodUID(uid))]
	return status.status, ok
}

func (m *manager) SetPodStatus(pod *v1.Pod, status v1.PodStatus) {
	m.podStatusLock.Lock()
	defer m.podStatusLock.Unlock()

	for _, c := range pod.Status.Conditions {
		if !kubetypes.PodConditionByKubelet(c.Type) {
			klog.Errorf("Kubelet is trying to update pod condition %q for pod %q. "+
				"But it is not owned by kubelet.", string(c.Type), format.Pod(pod))
		}
	}

	status = *status.DeepCopy()

	m.updateStatusInternal(pod, status, pod.DeletionTimestamp != nil)
}

func (m *manager) updateStatusInternal(pod *v1.Pod, status v1.PodStatus, forceUpdate bool) bool {
	var oldStatus v1.PodStatus
	cachedStatus, isCached := m.podStatuses[pod.UID]
	if isCached {
		oldStatus = cachedStatus.status
	} else if mirrorPod, ok := m.podManager.GetMirrorPodByPod(pod); ok {
		oldStatus = mirrorPod.Status
	} else {
		oldStatus = pod.Status
	}

	// TODO checkContainerStateTransition
	// Check for illegal state transition in containers
	//if err := checkContainerStateTransition(oldStatus.ContainerStatuses, status.ContainerStatuses, pod.Spec.RestartPolicy); err != nil {
	//	klog.Errorf("Status update on pod %v/%v aborted: %v", pod.Namespace, pod.Name, err)
	//	return false
	//}
	//if err := checkContainerStateTransition(oldStatus.InitContainerStatuses, status.InitContainerStatuses, pod.Spec.RestartPolicy); err != nil {
	//	klog.Errorf("Status update on pod %v/%v aborted: %v", pod.Namespace, pod.Name, err)
	//	return false
	//}

	updateLastTransitionTime(&status, &oldStatus, v1.ContainersReady)

	updateLastTransitionTime(&status, &oldStatus, v1.PodReady)

	updateLastTransitionTime(&status, &oldStatus, v1.PodInitialized)

	updateLastTransitionTime(&status, &oldStatus, v1.PodScheduled)

	if oldStatus.StartTime != nil && !oldStatus.StartTime.IsZero() {
		status.StartTime = oldStatus.StartTime
	} else if status.StartTime.IsZero() {
		now := metav1.Now()
		status.StartTime = &now
	}

	normalizeStatus(pod, &status)

	// The intent here is to prevent concurrent updates to a pod's status from
	// clobbering each other so the phase of a pod progresses monotonically.
	if isCached && isPodStatusByKubeletEqual(&cachedStatus.status, &status) && !forceUpdate {
		klog.V(3).Infof("Ignoring same status for pod %q, status: %+v", format.Pod(pod), status)
		return false // No new status.
	}

	newStatus := versionedPodStatus{
		status: 	status,
		version: 	cachedStatus.version  + 1,
		podName: 	pod.Name,
		podNamespace: 	pod.Namespace,
	}

	m.podStatuses[pod.UID] = newStatus

	select {
	case m.podStatusChannel <- podStatusSyncRequest{pod.UID, newStatus}:
		klog.V(5).Infof("Status Manager: adding pod: %q, with status: (%d, %v) to podStatusChannel",
			pod.UID, newStatus.version, newStatus.status)
		return true
	default:
	// Let the periodic syncBatch handle the update if the channel is full.
	// We can't block, since we hold the mutex lock.
		klog.V(4).Infof("Skipping the status update for pod %q for now because the channel is full; status: %+v",
			format.Pod(pod), status)
		return false
	}

	return false
}


// isPodStatusByKubeletEqual returns true if the given pod statuses are equal when non-kubelet-owned
// pod conditions are excluded.
// This method normalizes the status before comparing so as to make sure that meaningless
// changes will be ignored.
func isPodStatusByKubeletEqual(oldStatus, status *v1.PodStatus) bool {
	oldCopy := oldStatus.DeepCopy()
	for _, c := range status.Conditions {
		if kubetypes.PodConditionByKubelet(c.Type) {
			_, oc := podutil.GetPodCondition(oldCopy, c.Type)
			if oc == nil || oc.Status != c.Status || oc.Message != c.Message || oc.Reason != c.Reason {
				return false
			}
		}
	}
	oldCopy.Conditions = status.Conditions
	return apiequality.Semantic.DeepEqual(oldCopy, status)
}


// We add this function, because apiserver only supports *RFC3339* now, which means that the timestamp returned by
// apiserver has no nanosecond information. However, the timestamp returned by metav1.Now() contains nanosecond,
// so when we do comparison between status from apiserver and cached status, isPodStatusByKubeletEqual() will always return false.
// There is related issue #15262 and PR #15263 about this.
// In fact, the best way to solve this is to do it on api side. However, for now, we normalize the status locally in
// kubelet temporarily.
// TODO(random-liu): Remove timestamp related logic after apiserver supports nanosecond or makes it consistent.
func normalizeStatus(pod *v1.Pod, status *v1.PodStatus) *v1.PodStatus {
	bytesPerStatus := kubecontainer.MaxPodTerminationMessageLogLength
	if containers := len(pod.Spec.Containers) + len(pod.Spec.InitContainers); containers > 0 {
		bytesPerStatus = bytesPerStatus / containers
	}
	normalizeTimeStamp := func(t *metav1.Time) {
		*t = t.Rfc3339Copy()
	}
	normalizeContainerState := func(c *v1.ContainerState) {
		if c.Running != nil {
			normalizeTimeStamp(&c.Running.StartedAt)
		}
		if c.Terminated != nil {
			normalizeTimeStamp(&c.Terminated.StartedAt)
			normalizeTimeStamp(&c.Terminated.FinishedAt)
			if len(c.Terminated.Message) > bytesPerStatus {
				c.Terminated.Message = c.Terminated.Message[:bytesPerStatus]
			}
		}
	}

	if status.StartTime != nil {
		normalizeTimeStamp(status.StartTime)
	}
	for i := range status.Conditions {
		condition := &status.Conditions[i]
		normalizeTimeStamp(&condition.LastProbeTime)
		normalizeTimeStamp(&condition.LastTransitionTime)
	}

	// update container statuses
	for i := range status.ContainerStatuses {
		cstatus := &status.ContainerStatuses[i]
		normalizeContainerState(&cstatus.State)
		normalizeContainerState(&cstatus.LastTerminationState)
	}
	// Sort the container statuses, so that the order won't affect the result of comparison
	sort.Sort(kubetypes.SortedContainerStatuses(status.ContainerStatuses))

	// update init container statuses
	for i := range status.InitContainerStatuses {
		cstatus := &status.InitContainerStatuses[i]
		normalizeContainerState(&cstatus.State)
		normalizeContainerState(&cstatus.LastTerminationState)
	}
	// Sort the container statuses, so that the order won't affect the result of comparison
	kubetypes.SortInitContainerStatuses(pod, status.InitContainerStatuses)
	return status
}

func updateLastTransitionTime(status, oldStatus *v1.PodStatus, conditionType v1.PodConditionType) {
	_, condition := podutil.GetPodCondition(status, conditionType)
	if condition == nil {
		return
	}

	// Need to set LastTransitionTime.
	lastTransitionTime := metav1.Now()
	_, oldCondition := podutil.GetPodCondition(oldStatus, conditionType)
	if oldCondition != nil && condition.Status == oldCondition.Status {
		lastTransitionTime = oldCondition.LastTransitionTime
	}
	condition.LastTransitionTime = lastTransitionTime

}


func mergePodStatus(oldPodStatus, newPodStatus v1.PodStatus) v1.PodStatus {

	po, _ := json.MarshalIndent(oldPodStatus, "", "\t")
	pn, _ := json.MarshalIndent(oldPodStatus, "", "\t")

	klog.Infof("mergePodStatus oldPodStatus: %v, newPodStatus: %v", string(po), string(pn))

	podConditions := []v1.PodCondition{}
	for _, c := range oldPodStatus.Conditions {
		if !kubetypes.PodConditionByKubelet(c.Type) {
			podConditions = append(podConditions, c)
		}
	}
	for _, c := range newPodStatus.Conditions {
		if kubetypes.PodConditionByKubelet(c.Type) {
			podConditions = append(podConditions, c)
		}
	}
	newPodStatus.Conditions = podConditions
	return newPodStatus
}

















































