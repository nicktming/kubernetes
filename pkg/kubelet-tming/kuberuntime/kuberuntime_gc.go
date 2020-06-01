package kuberuntime

import (
	internalapi "k8s.io/cri-api/pkg/apis"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

type containerGC struct {
	client 			internalapi.RuntimeService
	manager 		*kubeGenericRuntimeManager
	podStateProvider 	podStateProvider
}

func newContainerGC(client internalapi.RuntimeService, podStateProvider podStateProvider, manager *kubeGenericRuntimeManager) *containerGC {
	return &containerGC{
		client: 		client,
		manager: 		manager,
		podStateProvider: 	podStateProvider,
	}
}

func (cgc *containerGC) GarbageCollect(gcPolicy kubecontainer.ContainerGCPolicy, allSourcesReady bool, evictTerminatedPods bool) error {
	errors := []error{}
	// Remove evictable containers
	//if err := cgc.evictContainers(gcPolicy, allSourcesReady, evictTerminatedPods); err != nil {
	//	errors = append(errors, err)
	//}

	// Remove sandboxes with zero containers
	//if err := cgc.evictSandboxes(evictTerminatedPods); err != nil {
	//	errors = append(errors, err)
	//}

	// Remove pod sandbox log directory
	//if err := cgc.evictPodLogsDirectories(allSourcesReady); err != nil {
	//	errors = append(errors, err)
	//}

	return utilerrors.NewAggregate(errors)
}