package status

import (
	"k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	//kubetypes "k8s.io/kubernetes/pkg/kubelet-new/types"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
	"sync"
	"k8s.io/apimachinery/pkg/api/errors"
	statusutil "k8s.io/kubernetes/pkg/util/pod"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	kubepod "k8s.io/kubernetes/pkg/kubelet-new/pod"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	kubetypes "k8s.io/kubernetes/pkg/kubelet-new/types"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	"time"
	"gopkg.in/square/go-jose.v2/json"
	"sort"
)

type Manager interface {
	PodStatusProvider

	Start()
	// SetPodStatus caches updates the cached status for the given pod, and triggers a status update.
	SetPodStatus(pod *v1.Pod, status v1.PodStatus)

	// SetContainerReadiness updates the cached container status with the given readiness, and
	// triggers a status update.
	SetContainerReadiness(podUID types.UID, containerID kubecontainer.ContainerID, ready bool)
}

// An object which provides guarantees that a pod can be safely deleted.
type PodDeletionSafetyProvider interface {
	// A function which returns true if the pod can safely be deleted.
	PodResourcesAreReclaimed(pod *v1.Pod, status v1.PodStatus) bool
}

// PodStatusProvider knows how to provide status for a pod. It's intended to be used by other components
// that need to introspect status.
type PodStatusProvider interface {
	// GetPodStatus returns the cached status for the provided pod UID, as well as whether it
	// was a cache hit.
	GetPodStatus(uid types.UID) (v1.PodStatus, bool)
}

type manager struct {
	kubeClient 			clientset.Interface
	podStatusesLock  		sync.RWMutex
	podDeletionSafetyProvider 	PodDeletionSafetyProvider
	podStatusChannel 		chan podStatusSyncRequest
	podManager 			kubepod.Manager
	podStatuses      		map[types.UID]versionedPodStatus
	apiStatusVersions 		map[types.UID]uint64
}

type versionedPodStatus struct {
	version 	uint64
	podStatus 	v1.PodStatus
}

type podStatusSyncRequest struct {
	podUID 		types.UID
	status 		versionedPodStatus
}

func NewManager(kubeClient clientset.Interface, podDeletionSafetyProvider PodDeletionSafetyProvider, podManager kubepod.Manager) Manager {
	return &manager {
		kubeClient: 			kubeClient,
		podDeletionSafetyProvider: 	podDeletionSafetyProvider,
		podStatusChannel: 		make(chan podStatusSyncRequest),
		podStatuses: 			make(map[types.UID]versionedPodStatus),
		podManager: 			podManager,
		apiStatusVersions: 		make(map[types.UID]uint64),
	}
}

const syncPeriod = 10 * time.Second

func (m *manager) Start() {
	if m.kubeClient == nil {
		klog.Infof("Kubernetes client is nil, not starting status manager.")
		return
	}
	klog.Info("Starting to sync pod status with apiserver")
	syncTicker := time.Tick(syncPeriod)
	// syncPod and syncBatch share the same go routine to avoid sync races.
	go wait.Forever(func() {
		select {
		case syncRequest := <-m.podStatusChannel:
			klog.Infof("Status Manager: syncing pod: %q, with status: (%v) from podStatusChannel",
				syncRequest.podUID, syncRequest.status)
			m.syncPod(syncRequest.podUID, syncRequest.status)
		case <-syncTicker:
			m.syncBatch()
		}
	}, 0)
}

func (m *manager) syncBatch() {
	var updatedStatuses []podStatusSyncRequest
	for podUID, status := range m.podStatuses {
		if m.needsUpdate(podUID, status) {
			updatedStatuses = append(updatedStatuses, podStatusSyncRequest{podUID, status})
		} else {
			klog.Infof("syncBatch Status for pod %q is up-to-date; skipping", podUID)
		}
	}
	for _, update := range updatedStatuses {
		klog.Infof("Status Manager: syncPod in syncbatch. pod UID: %q", update.podUID)
		m.syncPod(update.podUID, update.status)
	}
}

// needsUpdate returns whether the status is stale for the given pod UID.
// This method is not thread safe, and must only be accessed by the sync thread.
func (m *manager) needsUpdate(uid types.UID, status versionedPodStatus) bool {
	latest, ok := m.apiStatusVersions[uid]
	if !ok || latest < status.version {
		return true
	}
	pod, ok := m.podManager.GetPodByUID(uid)
	if !ok {
		return false
	}
	return m.canBeDeleted(pod, status.podStatus)
}

func (m *manager) SetPodStatus(pod *v1.Pod, status v1.PodStatus) {
	m.podStatusesLock.Lock()
	defer m.podStatusesLock.Unlock()

	//klog.Infof("setting pod %v status: %v", pod.UID, status)

	//for _, c := range pod.Status.Conditions {
	//	if !kubetypes.PodConditionByKubelet(c.Type) {
	//		klog.Errorf("Kubelet is trying to update pod condition %q for pod %q. "+
	//			"But it is not owned by kubelet.", string(c.Type), format.Pod(pod))
	//	}
	//}
	// Make sure we're caching a deep copy.
	status = *status.DeepCopy()

	// Force a status update if deletion timestamp is set. This is necessary
	// because if the pod is in the non-running state, the pod worker still
	// needs to be able to trigger an update and/or deletion.
	//m.updateStatusInternal(pod, status, pod.DeletionTimestamp != nil)

	m.updateStatusInternal(pod, status, pod.DeletionTimestamp != nil)
}

func updateLastTransitionTime(oldStatus, status *v1.PodStatus, conditionType v1.PodConditionType) {
	_, condition := podutil.GetPodCondition(status, conditionType)
	if condition == nil {
		return
	}
	// Need to set LastTransitionTime
	lastTransitionTime := metav1.Now()
	_, oldCondition := podutil.GetPodCondition(oldStatus, conditionType)


	if oldCondition != nil && oldCondition.Status == condition.Status {
		lastTransitionTime = oldCondition.LastTransitionTime
	}

	condition.LastTransitionTime = lastTransitionTime
	//pretty_status, _ := json.MarshalIndent(status, "", "\t")
	//if oldCondition != nil {
	//	klog.Infof("===>conditionType: %v, oldCondition.Status: %v, condition.Status: %v, time: %v\n, pretty status: %v",
	//		conditionType, oldCondition.Status, condition.Status, lastTransitionTime, string(pretty_status))
	//}
}

func (m *manager) updateStatusInternal(pod *v1.Pod, status v1.PodStatus, forceUpdate bool) bool {
	var oldStatus v1.PodStatus
	cacheStatus, isCache := m.podStatuses[pod.UID]
	if isCache {
		oldStatus = cacheStatus.podStatus
	} else {
		oldStatus = pod.Status
	}

	updateLastTransitionTime(&oldStatus, &status, v1.ContainersReady)

	updateLastTransitionTime(&oldStatus, &status, v1.PodReady)

	updateLastTransitionTime(&oldStatus, &status, v1.PodInitialized)

	updateLastTransitionTime(&oldStatus, &status, v1.PodScheduled)



	if oldStatus.StartTime != nil && !oldStatus.StartTime.IsZero() {
		status.StartTime = oldStatus.StartTime
	} else if status.StartTime == nil || status.StartTime.IsZero() {
		now := metav1.Now()
		status.StartTime = &now
	}

	//normalizeStatus(pod, &status)

	if isCache && isPodStatusByKubeletEqual(&oldStatus, &status) {
		//pretty_cachedStatus, _ := json.MarshalIndent(oldStatus, "", "\t")
		//pretty_status, _ := json.MarshalIndent(status, "", "\t")
		//klog.Infof("Ignoring same status for pod %q, cachedstatus: %v, statusfrompleg: %v",
		//	format.Pod(pod), string(pretty_cachedStatus), string(pretty_status))
		return false // No new status.
	}

	newStatus := versionedPodStatus{
		version: 	cacheStatus.version + 1,
		podStatus: 	status,
	}

	m.podStatuses[pod.UID] = newStatus

	select {
	case m.podStatusChannel <- podStatusSyncRequest{pod.UID, newStatus}:
		klog.V(5).Infof("Status Manager: adding pod: %q, with status: (%v) to podStatusChannel",
			pod.UID, status)
		return true
	default:
	// Let the periodic syncBatch handle the update if the channel is full.
	// We can't block, since we hold the mutex lock.
		klog.V(4).Infof("Skipping the status update for pod %q for now because the channel is full; status: %+v",
			format.Pod(pod), status)
		return false
	}
}

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


func (m *manager) syncPod(podUID types.UID, status versionedPodStatus) {
	if !m.needsUpdate(podUID, status) {
		klog.Infof("syncPod Status for pod %q is up-to-date; skipping", podUID)
		return
	}

	pod, _ := m.podManager.GetPodByUID(podUID)
	if pod == nil {
		panic("this should not happen")
	}

	pretty_status, _ := json.MarshalIndent(status.podStatus, "", "\t")
	klog.Infof("setting pod %v status: %v", pod.UID, string(pretty_status))

	// TODO: make me easier to express from client code
	pod, err := m.kubeClient.CoreV1().Pods(pod.Namespace).Get(pod.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		klog.V(3).Infof("Pod %q (%s) does not exist on the server", pod.Name, pod.UID)
		// If the Pod is deleted the status will be cleared in
		// RemoveOrphanedStatuses, so we just ignore the update here.
		return
	}
	if err != nil {
		klog.Warningf("Failed to get status for pod %q: %v", format.PodDesc(pod.Name, pod.Namespace, pod.UID), err)
		return
	}
	oldStatus := pod.Status.DeepCopy()
	newPod, patchBytes, err := statusutil.PatchPodStatus(m.kubeClient, pod.Namespace, pod.Name, *oldStatus, status.podStatus)
	klog.Infof("Patch status for pod %q with %q", format.Pod(pod), patchBytes)
	if err != nil {
		klog.Warningf("Failed to update status for pod %q: %v", format.Pod(pod), err)
		return
	}
	pod = newPod
	m.apiStatusVersions[pod.UID] = status.version

	klog.Infof("Status for pod %q updated successfully: (%d, %+v)", format.Pod(pod), status.version, status.podStatus)

	if m.canBeDeleted(pod, status.podStatus) {
		deleteOptions := metav1.NewDeleteOptions(0)
		deleteOptions.Preconditions = metav1.NewUIDPreconditions(string(pod.UID))
		err = m.kubeClient.CoreV1().Pods(pod.Namespace).Delete(pod.Name, deleteOptions)
		if err != nil {
			klog.Warningf("Failed to delete status for pod %q: %v", format.Pod(pod), err)
			return
		}
		klog.Infof("+++++++++++++Pod %q fully terminated and removed from etcd++++++++++", format.Pod(pod))
		// TODO deletePodStatus
		m.deletePodStatus(pod.UID)
	}
}

func (m *manager) deletePodStatus(uid types.UID) {
	m.podStatusesLock.Lock()
	defer m.podStatusesLock.Unlock()
	delete(m.podStatuses, uid)
}

func (m *manager) canBeDeleted(pod *v1.Pod, status v1.PodStatus) bool {
	if pod.DeletionTimestamp == nil {
		return false
	}
	delete := m.podDeletionSafetyProvider.PodResourcesAreReclaimed(pod, status)
	klog.Infof("can be deleted return %v\n", delete)
	return delete
}

//func mergePodStatus(oldPodStatus, newPodStatus v1.PodStatus) v1.PodStatus {
//	podCondi
//}


// isPodStatusByKubeletEqual returns true if the given pod statuses are equal when non-kubelet-owned
// pod conditions are excluded.
// This method normalizes the status before comparing so as to make sure that meaningless
// changes will be ignored.
func isPodStatusByKubeletEqual(oldStatus, status *v1.PodStatus) bool {
	oldCopy := oldStatus.DeepCopy()
	//for _, c := range status.Conditions {
	//	if kubetypes.PodConditionByKubelet(c.Type) {
	//		_, oc := podutil.GetPodCondition(oldCopy, c.Type)
	//		if oc == nil || oc.Status != c.Status || oc.Message != c.Message || oc.Reason != c.Reason {
	//			return false
	//		}
	//	}
	//}
	oldCopy.Conditions = status.Conditions
	return apiequality.Semantic.DeepEqual(oldCopy, status)
}





func (m *manager) GetPodStatus(uid types.UID) (v1.PodStatus, bool) {
	m.podStatusesLock.RLock()
	defer m.podStatusesLock.RUnlock()
	//status, ok := m.podStatuses[types.UID(m.podManager.TranslatePodUID(uid))]
	// TODO TranslatePodUID
	status, ok := m.podStatuses[types.UID(uid)]
	return status.podStatus, ok
}


func (m *manager) SetContainerReadiness(podUID types.UID, containerID kubecontainer.ContainerID, ready bool) {
	m.podStatusesLock.Lock()
	defer m.podStatusesLock.Unlock()

	pod, ok := m.podManager.GetPodByUID(podUID)
	if !ok {
		klog.V(4).Infof("Pod %q has been deleted, no need to update readiness", string(podUID))
		return
	}

	oldStatus, found := m.podStatuses[pod.UID]
	if !found {
		klog.Warningf("Container readiness changed before pod has synced: %q - %q",
			format.Pod(pod), containerID.String())
		return
	}

	// Find the container to update.
	containerStatus, _, ok := findContainerStatus(&oldStatus.podStatus, containerID.String())
	if !ok {
		klog.Warningf("Container readiness changed for unknown container: %q - %q",
			format.Pod(pod), containerID.String())
		return
	}

	if containerStatus.Ready == ready {
		klog.V(4).Infof("Container readiness unchanged (%v): %q - %q", ready,
			format.Pod(pod), containerID.String())
		return
	}

	// Make sure we're not updating the cached version.
	status := *oldStatus.podStatus.DeepCopy()
	containerStatus, _, _ = findContainerStatus(&status, containerID.String())
	containerStatus.Ready = ready

	// updateConditionFunc updates the corresponding type of condition
	updateConditionFunc := func(conditionType v1.PodConditionType, condition v1.PodCondition) {
		conditionIndex := -1
		for i, condition := range status.Conditions {
			if condition.Type == conditionType {
				conditionIndex = i
				break
			}
		}
		if conditionIndex != -1 {
			status.Conditions[conditionIndex] = condition
		} else {
			klog.Warningf("PodStatus missing %s type condition: %+v", conditionType, status)
			status.Conditions = append(status.Conditions, condition)
		}
	}
	updateConditionFunc(v1.PodReady, GeneratePodReadyCondition(&pod.Spec, status.Conditions, status.ContainerStatuses, status.Phase))
	updateConditionFunc(v1.ContainersReady, GenerateContainersReadyCondition(&pod.Spec, status.ContainerStatuses, status.Phase))
	m.updateStatusInternal(pod, status, false)
}




func findContainerStatus(status *v1.PodStatus, containerID string) (containerStatus *v1.ContainerStatus, init bool, ok bool) {
	// Find the container to update.
	for i, c := range status.ContainerStatuses {
		if c.ContainerID == containerID {
			return &status.ContainerStatuses[i], false, true
		}
	}

	for i, c := range status.InitContainerStatuses {
		if c.ContainerID == containerID {
			return &status.InitContainerStatuses[i], true, true
		}
	}

	return nil, false, false

}

// NeedToReconcilePodReadiness returns if the pod "Ready" condition need to be reconcile
func NeedToReconcilePodReadiness(pod *v1.Pod) bool {
	if len(pod.Spec.ReadinessGates) == 0 {
		return false
	}
	podReadyCondition := GeneratePodReadyCondition(&pod.Spec, pod.Status.Conditions, pod.Status.ContainerStatuses, pod.Status.Phase)
	i, curCondition := podutil.GetPodConditionFromList(pod.Status.Conditions, v1.PodReady)
	// Only reconcile if "Ready" condition is present and Status or Message is not expected
	if i >= 0 && (curCondition.Status != podReadyCondition.Status || curCondition.Message != podReadyCondition.Message) {
		return true
	}
	return false
}










































