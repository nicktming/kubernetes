package kubelet_tming


import (
	cadvisorapiv1 "github.com/google/cadvisor/info/v1"
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet-tming/cm"
)

// GetCachedMachineInfo assumes that the machine info can't change without a reboot
func (kl *Kubelet) GetCachedMachineInfo() (*cadvisorapiv1.MachineInfo, error) {
	return kl.machineInfo, nil
}

func (kl *Kubelet) getNodeAnyWay() (*v1.Node, error) {
	if kl.kubeClient != nil {
		if n, err := kl.nodeInfo.GetNodeInfo(string(kl.nodeName)); err == nil {
			return n, nil
		}
	}
	return kl.initialNode()
}

// GetNodeConfig returns the container manager node config.
func (kl *Kubelet) GetNodeConfig() cm.NodeConfig {
	return kl.containerManager.GetNodeConfig()
}

// GetPodByCgroupfs provides the pod that maps to the specified cgroup, as well
// as whether the pod was found.
func (kl *Kubelet) GetPodByCgroupfs(cgroupfs string) (*v1.Pod, bool) {
	//pcm := kl.containerManager.NewPodContainerManager()
	//if result, podUID := pcm.IsPodCgroup(cgroupfs); result {
	//	return kl.podManager.GetPodByUID(podUID)
	//}
	return nil, false
}
