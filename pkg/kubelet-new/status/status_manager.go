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
)

type Manager interface {
	// SetPodStatus caches updates the cached status for the given pod, and triggers a status update.
	SetPodStatus(pod *v1.Pod, status v1.PodStatus)
}

type manager struct {
	kubeClient clientset.Interface
	podStatusesLock  sync.RWMutex
}

func NewManager(kubeClient clientset.Interface) Manager {
	return &manager {
		kubeClient: 	kubeClient,
	}
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

	m.syncPod(pod, status)
}

func (m *manager) syncPod(pod *v1.Pod, status v1.PodStatus) {
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
	newPod, patchBytes, err := statusutil.PatchPodStatus(m.kubeClient, pod.Namespace, pod.Name, *oldStatus, status)
	klog.Infof("Patch status for pod %q with %q", format.Pod(pod), patchBytes)
	if err != nil {
		klog.Warningf("Failed to update status for pod %q: %v", format.Pod(pod), err)
		return
	}
	pod = newPod

	klog.Infof("Status for pod %q updated successfully: (, %+v)", format.Pod(pod), status)
}

//func mergePodStatus(oldPodStatus, newPodStatus v1.PodStatus) v1.PodStatus {
//	podCondi
//}






















































