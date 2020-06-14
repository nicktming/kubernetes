package kuberuntime

import (
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/api/core/v1"
	//utilfeature "k8s.io/apiserver/pkg/util/feature"
	//kubefeatures "k8s.io/kubernetes/pkg/features"
	//"time"
)

func (m *kubeGenericRuntimeManager) applyPlatformSpecificContainerConfig(config *runtimeapi.ContainerConfig, container *v1.Container, pod *v1.Pod, uid *int64, username string) error {
	config.Linux = m.generateLinuxContainerConfig(container, pod, uid, username)
	return nil
}

// generateLinuxContainerConfig generates linux container config for kubelet runtime v1.
func (m *kubeGenericRuntimeManager) generateLinuxContainerConfig(container *v1.Container, pod *v1.Pod, uid *int64, username string) *runtimeapi.LinuxContainerConfig {
	lc := &runtimeapi.LinuxContainerConfig{
		Resources: 		&runtimeapi.LinuxContainerResources{},
		//SecurityContext: m.determineEffectiveSecurityContext(pod, container, uid, username),
	}

	// set linux container resources
	var cpuShares int64
	cpuRequest := container.Resources.Requests.Cpu()
	cpuLimit   := container.Resources.Limits.Cpu()
	memoryLimit := container.Resources.Limits.Memory().Value()
	//oomScoreAdj := int64(qos.GetContainerOOMScoreAdjust(pod, container,
	//	int64(m.machineInfo.MemoryCapacity)))

	if cpuRequest.IsZero() && !cpuLimit.IsZero() {
		cpuShares = milliCPUToShares(cpuLimit.MilliValue())
	} else {
		cpuShares = milliCPUToShares(cpuRequest.MilliValue())
	}
	lc.Resources.CpuShares = cpuShares
	if memoryLimit != 0 {
		lc.Resources.MemoryLimitInBytes = memoryLimit
	}

	// TODO oom
	// Set OOM score of the container based on qos policy. Processes in lower-priority pods should
	// be killed first if the system runs out of memory.
	// lc.Resources.OomScoreAdj = oomScoreAdj

	//if m.cpuCFSQuota {
	//	// if cpuLimit.Amount is nil, then the appropriate default value is returned
	//	// to allow full usage of cpu resource.
	//	cpuPeriod := int64(quotaPeriod)
	//	if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.CPUCFSQuotaPeriod) {
	//		cpuPeriod = int64(m.cpuCFSQuotaPeriod.Duration / time.Microsecond)
	//	}
	//	cpuQuota := milliCPUToQuota(cpuLimit.MilliValue(), cpuPeriod)
	//	lc.Resources.CpuQuota = cpuQuota
	//	lc.Resources.CpuPeriod = cpuPeriod
	//}

	return lc
}
