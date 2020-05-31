package cm

import (
	"os"
	"fmt"

	libcontainercgroups "github.com/opencontainers/runc/libcontainer/cgroups"
)

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
