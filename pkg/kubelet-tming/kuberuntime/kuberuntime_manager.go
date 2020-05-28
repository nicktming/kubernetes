package kuberuntime

import (
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/kubelet-tming/images"
	internalapi "k8s.io/cri-api/pkg/apis"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	kubetypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
)

type kubeGenericRuntimeManager struct {
	runtimeName 		string
	recorder 		record.EventRecorder
	// wrapped image puller.
	imagePuller images.ImageManager

	// gRPC service clients
	runtimeService internalapi.RuntimeService
	imageService   internalapi.ImageManagerService

}

type KubeGenericRuntime interface {
	kubecontainer.Runtime

	// TODO
	// kubecontainer.StreamingRuntime
	// kubecontainer.ContainerCommandRunner
}

func NewKubeGenericRuntimeManager(
			runtimeService internalapi.RuntimeService,
			imageService internalapi.ImageManagerService,
			) (KubeGenericRuntime, error) {
	kubeRuntimeManager := &kubeGenericRuntimeManager{
		runtimeService:		runtimeService,
		imageService:		imageService,
	}

	return kubeRuntimeManager, nil
}


func (m *kubeGenericRuntimeManager) GetPods(all bool) ([]*kubecontainer.Pod, error) {
	pods := make(map[kubetypes.UID]*kubecontainer.Pod)

	sandboxes, err := m.getKubeletSandboxes(all)
	if err != nil {
		return nil, err
	}

	for i := range sandboxes {
		s := sandboxes[i]
		if s.Metadata == nil {
			klog.Infof("Sandbox does not have metadata: %+v", s)
			continue
		}

		podUID := kubetypes.UID(s.Metadata.Uid)
		if _, ok := pods[podUID]; !ok {
			pods[podUID] = &kubecontainer.Pod{
				ID: 		podUID,
				Name: 		s.Metadata.Name,
				Namespace:	s.Metadata.Namespace,
			}
		}
		p := pods[podUID]
		converted, err := m.sandboxToKubeContainer(s)
		if err != nil {
			klog.V(4).Infof("Convert %q sandbox %v of pod %q failed: %v", m.runtimeName, s, podUID, err)
			continue
		}
		p.Sandboxes = append(p.Sandboxes, converted)
	}

	containers, err := m.getKubeletContainers(all)
	if err != nil {
		return nil, err
	}

	for i := range containers {
		c := containers[i]
		if c.Metadata == nil {
			klog.Infof("Container does not have metadata: %+v", c)
			continue
		}

		labelledInfo := getContainerInfoFromLabels(c.Labels)
		pod, found := pods[labelledInfo.PodUID]
		if !found {
			pod = &kubecontainer.Pod{
				ID: 		labelledInfo.PodUID,
				Name:		labelledInfo.PodName,
				Namespace: 	labelledInfo.PodNamespace,
			}
			pods[labelledInfo.PodUID] = pod
		}

		converted, err := m.toKubeContainer(c)
		if err != nil {
			klog.V(4).Infof("Convert %s container %v of pod %q failed: %v", m.runtimeName, c, labelledInfo.PodUID, err)
			continue
		}

		pod.Containers = append(pod.Containers, converted)
	}

	var result []*kubecontainer.Pod
	for _, pod := range pods {
		result = append(result, pod)
	}

	return result, nil

}