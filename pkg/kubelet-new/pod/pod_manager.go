package pod

import (
	"k8s.io/api/core/v1"
	"sync"
	//kubetypes "k8s.io/kubernetes/pkg/kubelet-new/types"
	"k8s.io/apimachinery/pkg/types"
)

type Manager interface {

	// AddPod adds the given pod to the manager.
	AddPod(pod *v1.Pod)

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

func (pm *basicManager) GetPodByUID(pid types.UID) (*v1.Pod, bool) {
	return pm.podByUID[pid]
}


