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
	"time"
	"gopkg.in/square/go-jose.v2/json"
)

type Manager interface {
	Start()
	// SetPodStatus caches updates the cached status for the given pod, and triggers a status update.
	SetPodStatus(pod *v1.Pod, status v1.PodStatus)
}

// An object which provides guarantees that a pod can be safely deleted.
type PodDeletionSafetyProvider interface {
	// A function which returns true if the pod can safely be deleted.
	PodResourcesAreReclaimed(pod *v1.Pod, status v1.PodStatus) bool
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

func updateLastTransitionTime(status, oldStatus *v1.PodStatus, conditionType v1.PodConditionType) {
	_, condition := podutil.GetPodCondition(status, conditionType)
	if condition == nil {
		return
	}
	// Need to set LastTransitionTime
	lastTransitionTime := metav1.Now()
	_, oldCondition := podutil.GetPodCondition(oldStatus, conditionType)

	klog.Infof("===>oldCondition.Status: %v, condition.Status: %v", oldCondition.Status, condition.Status)

	if oldCondition != nil && oldCondition.Status == condition.Status {
		lastTransitionTime = oldCondition.LastTransitionTime
	}
	condition.LastTransitionTime = lastTransitionTime
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




















































