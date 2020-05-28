package kuberuntime


import (
	"k8s.io/klog"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)


func (m *kubeGenericRuntimeManager) getKubeletContainers(allContainers bool) ([]*runtimeapi.Container, error) {
	filter := &runtimeapi.ContainerFilter{}

	if !allContainers {
		filter.State = &runtimeapi.ContainerStateValue{
			State:		runtimeapi.ContainerState_CONTAINER_RUNNING,
		}
	}

	containers, err := m.runtimeService.ListContainers(filter)
	if err != nil {
		klog.Errorf("getKubeletContainers failed: %v", err)
		return nil, err
	}

	return containers, nil
}
