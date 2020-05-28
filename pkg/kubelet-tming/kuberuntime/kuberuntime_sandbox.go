package kuberuntime

import (
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog"
)

func (m *kubeGenericRuntimeManager) getKubeletSandboxes(all bool) ([]*runtimeapi.PodSandbox, error) {
	var filter *runtimeapi.PodSandboxFilter

	if !all {
		readyState := runtimeapi.PodSandboxState_SANDBOX_READY

		filter = &runtimeapi.PodSandboxFilter {
			State: 		&runtimeapi.PodSandboxStateValue{
				State:		readyState,
			},
		}
	}

	resp, err := m.runtimeService.ListPodSandbox(filter)
	if err != nil {
		klog.Errorf("ListPodSandbox failed: %v", err)
		return nil, err
	}

	return resp, nil
}