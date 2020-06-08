package cm


import (
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/kubernetes/pkg/kubelet/stats/pidlimit"
	"k8s.io/klog"
)


const (
	defaultNodeAllocatableCgroupName = "kubepods"
)

// createNodeAllocatableCgroups create Node Allocatable Cgroup when CgroupsPerQOS flag is specified as true
func (cm *containerManagerImpl) createNodeAllocatableCgroups() error {
	klog.Infof("container manager createNodeAllocatableCgroups: %v", cm.cgroupRoot)

	cgroupConfig := &CgroupConfig{
		Name: 				cm.cgroupRoot,
		ResourceParameters:		getCgroupConfig(cm.internalCapacity),
	}

	if cm.cgroupManager.Exists(cgroupConfig.Name) {
		return nil
	}

	if err := cm.cgroupManager.Create(cgroupConfig); err != nil {
		klog.Errorf("Failed to create %q cgroup", cm.cgroupRoot)
		return err
	}
	return nil
}


func (cm *containerManagerImpl) GetNodeAllocatableReservation() v1.ResourceList {
	evictionReservation := hardEvictionReservation(cm.HardEvictionThresholds, cm.capacity)
	result := make(v1.ResourceList)

	for k := range cm.capacity {
		value := resource.NewQuantity(0, resource.DecimalSI)
		if cm.NodeConfig.SystemReserved != nil {
			value.Add(cm.NodeConfig.SystemReserved[k])
		}
		if cm.NodeConfig.KubeReserved != nil {
			value.Add(cm.NodeConfig.KubeReserved[k])
		}
		if evictionReservation != nil {
			value.Add(evictionReservation[k])
		}
		if !value.IsZero() {
			result[k] = *value
		}
	}
	return result
}

// getCgroupConfig returns a ResourceConfig object that can be used to create or update cgroups via CgroupManager interface.
func getCgroupConfig(rl v1.ResourceList) *ResourceConfig {
	// TODO(vishh): Set CPU Quota if necessary.
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
	if q, exists := rl[pidlimit.PIDs]; exists {
		val := q.Value()
		rc.PidsLimit = &val
	}
	rc.HugePageLimit = HugePageLimits(rl)

	return &rc
}