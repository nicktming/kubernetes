package cm

import (
	"os"
	"fmt"
	"k8s.io/apimachinery/pkg/types"

	libcontainercgroups "github.com/opencontainers/runc/libcontainer/cgroups"
	"k8s.io/api/core/v1"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
)

func GetPodCgroupNameSuffix(podUID types.UID) string {
	return podCgroupNamePrefix + string(podUID)
}

// HugePageLimits converts the API representation to a map
// from huge page size (in bytes) to huge page limit (in bytes).
func HugePageLimits(resourceList v1.ResourceList) map[int64]int64 {
	hugePageLimits := map[int64]int64{}
	for k, v := range resourceList {
		if v1helper.IsHugePageResourceName(k) {
			pageSize, _ := v1helper.HugePageSizeFromResourceName(k)
			if value, exists := hugePageLimits[pageSize.Value()]; exists {
				hugePageLimits[pageSize.Value()] = value + v.Value()
			} else {
				hugePageLimits[pageSize.Value()] = v.Value()
			}
		}
	}
	return hugePageLimits
}

func GetCgroupSubsystems() (*CgroupSubsystems, error) {
	allCgroups, err := libcontainercgroups.GetCgroupMounts(true)
	if err != nil {
		return &CgroupSubsystems{}, err
	}

	if len(allCgroups) == 0 {
		return &CgroupSubsystems{}, fmt.Errorf("failed to find cgroup mounts")
	}

	mountPoints := make(map[string]string, len(allCgroups))
	for _, mount := range allCgroups {
		for _, subsystem := range mount.Subsystems {
			mountPoints[subsystem] = mount.Mountpoint
		}
	}

	return &CgroupSubsystems{
		Mounts:		allCgroups,
		MountPoints: 	mountPoints,
	}, nil
}

func NodeAllocatableRoot(cgroupRoot, cgroupDriver string) string {
	root := ParseCgroupfsToCgroupName(cgroupRoot)
	nodeAllocatableRoot := NewCgroupName(root, defaultNodeAllocatableCgroupName)

	if libcontainerCgroupManagerType(cgroupDriver) == libcontainerSystemd {
		return nodeAllocatableRoot.ToSystemd()
	}
	return nodeAllocatableRoot.ToCgroupfs()
}


func GetKubeletContainer(kubeletCgroups string) (string, error) {
	if kubeletCgroups == "" {
		cont, err := getContainer(os.Getpid())
		if err != nil {
			return "", err
		}
		return cont, nil
	}
	return kubeletCgroups, nil
}


func GetRuntimeContainer(containerRuntime, runtimeCgroups string) (string, error) {
	if containerRuntime == "docker" {
		cont, err := getContainerNameForProcess(dockerProcessName, dockerPidFile)
		if err != nil {
			return "", fmt.Errorf("failed to get container name for docker process: %v", err)
		}
		return cont, nil
	}
	return runtimeCgroups, nil
}
