package prober

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/api/core/v1"
)

type Manager interface {
	// Update Pod Status
	UpdatePodStatus(types.UID, *v1.PodStatus)
}


type manager struct {

}

func NewManager() Manager {
	return &manager{}
}

func (m *manager) UpdatePodStatus(podUID types.UID, podStatus *v1.PodStatus) {
	for i, c := range podStatus.ContainerStatuses {
		var ready bool
		if c.State.Running != nil {
			ready = true
		}
		podStatus.ContainerStatuses[i].Ready = ready
	}
	// TODO initcontainer
}

