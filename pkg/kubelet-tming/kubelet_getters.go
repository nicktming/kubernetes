package kubelet_tming


import (
	cadvisorapiv1 "github.com/google/cadvisor/info/v1"
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet-tming/cm"
	"net"
	utilnode "k8s.io/kubernetes/pkg/util/node"
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

// GetPodByFullName gets the pod with the given 'full' name, which
// incorporates the namespace as well as whether the pod was found.
func (kl *Kubelet) GetPodByFullName(podFullName string) (*v1.Pod, bool) {
	return kl.podManager.GetPodByFullName(podFullName)
}

// GetPodByName provides the first pod that matches namespace and name, as well
// as whether the pod was found.
func (kl *Kubelet) GetPodByName(namespace, name string) (*v1.Pod, bool) {
	return kl.podManager.GetPodByName(namespace, name)
}

// GetPodCgroupRoot returns the listeral cgroupfs value for the cgroup containing all pods
func (kl *Kubelet) GetPodCgroupRoot() string {
	return kl.containerManager.GetPodCgroupRoot()
}

// GetPods returns all pods bound to the kubelet and their spec, and the mirror
// pods.
func (kl *Kubelet) GetPods() []*v1.Pod {
	pods := kl.podManager.GetPods()
	// a kubelet running without apiserver requires an additional
	// update of the static pod status. See #57106
	for _, p := range pods {
		if status, ok := kl.statusManager.GetPodStatus(p.UID); ok {
			p.Status = status
		}
	}
	return pods
}

// getHostIPAnyway attempts to return the host IP from kubelet's nodeInfo, or
// the initialNode.
func (kl *Kubelet) getHostIPAnyWay() (net.IP, error) {
	node, err := kl.getNodeAnyWay()
	if err != nil {
		return nil, err
	}
	return utilnode.GetNodeHostIP(node)
}

