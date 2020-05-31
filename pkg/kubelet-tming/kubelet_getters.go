package kubelet_tming


import (
	cadvisorapiv1 "github.com/google/cadvisor/info/v1"
	"k8s.io/api/core/v1"
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
