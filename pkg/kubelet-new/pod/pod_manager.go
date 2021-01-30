package pod

import (
	"k8s.io/api/core/v1"
	"sync"
	//kubetypes "k8s.io/kubernetes/pkg/kubelet-new/types"
	"k8s.io/apimachinery/pkg/types"
)

type Manager interface {
	// GetPods returns the regular pods bound to the kubelet and their spec.
	GetPods() []*v1.Pod

	// AddPod adds the given pod to the manager.
	AddPod(pod *v1.Pod)

	// UpdatePod updates the given pod in the manager.
	UpdatePod(pod *v1.Pod)

	// DeletePod deletes the given pod from the manager.  For mirror pods,
	// this means deleting the mappings related to mirror pods.  For non-
	// mirror pods, this means deleting from indexes for all non-mirror pods.
	DeletePod(pod *v1.Pod)

	// GetPodByUID provides the (non-mirror) pod that matches pod UID, as well as
	// whether the pod is found.
	GetPodByUID(types.UID) (*v1.Pod, bool)
}

type basicManager struct {
	lock sync.RWMutex

	podByUID map[types.UID]*v1.Pod
}

func NewBasicPodManager() Manager {
	return &basicManager{
		podByUID: make(map[types.UID]*v1.Pod),
	}
}

func (pm *basicManager) AddPod(pod *v1.Pod) {
	pm.podByUID[pod.UID] = pod
}

func (pm *basicManager) UpdatePod(pod *v1.Pod) {
	pm.podByUID[pod.UID] = pod
}

func (pm *basicManager) DeletePod(pod *v1.Pod) {
	delete(pm.podByUID, pod.UID)
}

func (m *basicManager) GetPods() []*v1.Pod {
	var pods []*v1.Pod
	for _, pod := range m.podByUID {
		pods = append(pods, pod)
	}
	return pods
}

func (pm *basicManager) GetPodByUID(pid types.UID) (*v1.Pod, bool) {
	p, ok := pm.podByUID[pid]
	return p, ok
}


