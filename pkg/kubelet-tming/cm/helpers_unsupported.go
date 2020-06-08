package cm


import (
	"k8s.io/apimachinery/pkg/types"
)

const (
	MinShares     = 0
	SharesPerCPU  = 0
	MilliCPUToCPU = 0

	MinQuotaPeriod = 0
)

// MilliCPUToQuota converts milliCPU and period to CFS quota values.
func MilliCPUToQuota(milliCPU, period int64) int64 {
	return 0
}

// MilliCPUToShares converts the milliCPU to CFS shares.
func MilliCPUToShares(milliCPU int64) int64 {
	return 0
}

// ResourceConfigForPod takes the input pod and outputs the cgroup resource config.
//func ResourceConfigForPod(pod *v1.Pod, enforceCPULimit bool, cpuPeriod uint64) *ResourceConfig {
//	return nil
//}
//
//// GetCgroupSubsystems returns information about the mounted cgroup subsystems
//func GetCgroupSubsystems() (*CgroupSubsystems, error) {
//	return nil, nil
//}

func getCgroupProcs(dir string) ([]int, error) {
	return nil, nil
}

// GetPodCgroupNameSuffix returns the last element of the pod CgroupName identifier
//func GetPodCgroupNameSuffix(podUID types.UID) string {
//	return ""
//}

// NodeAllocatableRoot returns the literal cgroup path for the node allocatable cgroup
//func NodeAllocatableRoot(cgroupRoot, cgroupDriver string) string {
//	return ""
//}

// GetKubeletContainer returns the cgroup the kubelet will use
//func GetKubeletContainer(kubeletCgroups string) (string, error) {
//	return "", nil
//}

// GetRuntimeContainer returns the cgroup used by the container runtime
//func GetRuntimeContainer(containerRuntime, runtimeCgroups string) (string, error) {
//	return "", nil
//}
