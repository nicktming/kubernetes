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
	"time"
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
		podStatuses: 			make(map[types.UID]v1.PodStatus),
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
	if !ok {
		return false
	}
	if latest < status.version {
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

	oldStatus, ok := m.podStatuses[pod.UID]
	if !ok {
		oldStatus = versionedPodStatus{
		}
	}

	newStatus := versionedPodStatus{
		version: 	oldStatus.version + 1,
		podStatus: 	status,
	}

	m.podStatuses[pod.UID] = newStatus

	m.updateStatusInternal(pod, newStatus, pod.DeletionTimestamp != nil)
}

func (m *manager) updateStatusInternal(pod *v1.Pod, status versionedPodStatus, forceUpdate bool) bool {
	select {
	case m.podStatusChannel <- podStatusSyncRequest{pod.UID, status}:
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
		klog.V(1).Infof("Status for pod %q is up-to-date; skipping", podUID)
		return
	}

	pod, _ := m.podManager.GetPodByUID(podUID)
	if pod == nil {
		panic("this should not happen")
	}

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
	m.podStatuses[pod.UID] = status

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
	return m.podDeletionSafetyProvider.PodResourcesAreReclaimed(pod, status)
}

//func mergePodStatus(oldPodStatus, newPodStatus v1.PodStatus) v1.PodStatus {
//	podCondi
//}






















































