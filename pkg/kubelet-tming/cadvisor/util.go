package cadvisor

import (
	goruntime "runtime"

	cadvisorapi "github.com/google/cadvisor/info/v1"
	cadvisorapi2 "github.com/google/cadvisor/info/v2"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	kubetypes "k8s.io/kubernetes/pkg/kubelet-tming/types"
)


const (
	CrioSocket = "/var/run/crio/crio.sock"
)

func CapacityFromMachineInfo(info *cadvisorapi.MachineInfo) v1.ResourceList {
	c := v1.ResourceList {
		v1.ResourceCPU: *resource.NewMilliQuantity(
			int64(info.NumCores * 1000),
			resource.DecimalSI,
		),
		v1.ResourceMemory: *resource.NewQuantity(
			int64(info.MemoryCapacity),
			resource.BinarySI,
		),
	}

	// if huge pages are enabled, we report them as a schedulable resource on the node
	for _, hugepagesInfo := range info.HugePages {
		pageSizeBytes := int64(hugepagesInfo.PageSize * 1024)
		hugePagesBytes := pageSizeBytes * int64(hugepagesInfo.NumPages)
		pageSizeQuantity := resource.NewQuantity(pageSizeBytes, resource.BinarySI)
		c[v1helper.HugePageResourceName(*pageSizeQuantity)] = *resource.NewQuantity(hugePagesBytes, resource.BinarySI)
	}

	return c
}

func EphemeralStorageCapacityFromFsInfo(info cadvisorapi2.FsInfo) v1.ResourceList {
	c := v1.ResourceList{
		v1.ResourceEphemeralStorage: *resource.NewQuantity(
			int64(info.Capacity),
			resource.BinarySI),
	}
	return c
}

func UsingLegacyCadvisorStats(runtime, runtimeEndpoint string) bool {
	return (runtime == kubetypes.DockerContainerRuntime && goruntime.GOOS == "linux") ||
		runtimeEndpoint == CrioSocket || runtimeEndpoint == "unix://"+CrioSocket
}


