package cm

import (
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
	"k8s.io/kubernetes/pkg/kubelet/events"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"k8s.io/api/core/v1"
	"fmt"
)

const (
	defaultNodeAllocatableCgroupName = "kubepods"
)



// enforceNodeAllocatableCgroups enforce Node Allocatable Cgroup settings.
func (cm *containerManagerImpl) enforceNodeAllocatableCgroups() error {

	nc := cm.NodeConfig.NodeAllocatableConfig

	// Using ObjectReference for events as the node maybe not cached; refer to #42701 for detail.
	nodeRef := &v1.ObjectReference{
		Kind:      "Node",
		Name:      cm.nodeInfo.Name,
		UID:       types.UID(cm.nodeInfo.Name),
		Namespace: "",
	}

	//cgroupConfig := &CgroupConfig{
	//	Name:               cm.cgroupRoot,
	//	ResourceParameters: getCgroupConfig(nodeAllocatable),
	//}

	if nc.EnforceNodeAllocatable.Has(kubetypes.KubeReservedEnforcementKey) {
		klog.V(2).Infof("Enforcing kube reserved on cgroup %q with limits: %+v", nc.KubeReservedCgroupName, nc.KubeReserved)
		if err := enforceExistingCgroup(cm.cgroupManager, ParseCgroupfsToCgroupName(nc.KubeReservedCgroupName), nc.KubeReserved); err != nil {
			message := fmt.Sprintf("Failed to enforce Kube Reserved Cgroup Limits on %q: %v", nc.KubeReservedCgroupName, err)
			cm.recorder.Event(nodeRef, v1.EventTypeWarning, events.FailedNodeAllocatableEnforcement, message)
			return fmt.Errorf(message)
		}
		cm.recorder.Eventf(nodeRef, v1.EventTypeNormal, events.SuccessfulNodeAllocatableEnforcement, "Updated limits on kube reserved cgroup %v", nc.KubeReservedCgroupName)
	}


	return nil
}

func getCgroupConfig(rl v1.ResourceList) *ResourceConfig {
	if rl == nil {
		return nil
	}
	var rc ResourceConfig
	if q, exists := rl[v1.ResourceMemory]; exists {
		// Memory is defined in bytes.
		val := q.Value()
		rc.Memory = &val
	}
	if q, exists := rl[v1.ResourceCPU]; exists {
		// CPU is defined in milli-cores.
		val := MilliCPUToShares(q.MilliValue())
		rc.CpuShares = &val
	}
	// TODO pid limit
	return &rc
}

// enforceExistingCgroup updates the limits `rl` on existing cgroup `cName` using `cgroupManager` interface.
func enforceExistingCgroup(cgroupManager CgroupManager, cName CgroupName, rl v1.ResourceList) error {
	cgroupConfig := &CgroupConfig {
		Name: 			cName,
		ResourceParameters: 	getCgroupConfig(rl),
	}
	if cgroupConfig.ResourceParameters == nil {
		return fmt.Errorf("%q cgroup is not config properly", cgroupConfig.Name)
	}
	//klog.V(4).Infof("Enforcing limits on cgroup %q with %d cpu shares, %d bytes of memory, and %d processes", cName, cgroupConfig.ResourceParameters.CpuShares, cgroupConfig.ResourceParameters.Memory, cgroupConfig.ResourceParameters.PidsLimit)
	if !cgroupManager.Exists(cgroupConfig.Name) {
		return fmt.Errorf("%q cgroup does not exist", cgroupConfig.Name)
	}
	if err := cgroupManager.Update(cgroupConfig); err != nil {
		return err
	}
	return nil
}