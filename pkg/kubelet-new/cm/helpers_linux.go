package cm

import (
	libcontainercgroups "github.com/opencontainers/runc/libcontainer/cgroups"
	"strings"
	"fmt"
	"os"
)

// NewCgroupName composes a new cgroup name.
// Use RootCgroupName as base to start at the root.
// This function does some basic check for invalid characters at the name.
func NewCgroupName(base CgroupName, components ...string) CgroupName {
	for _, component := range components {
		// Forbit using "_" in internal names. When remapping internal
		// names to systemd cgroup driver, we want to remap "-" => "_",
		// so we forbid "_" so that we can always reverse the mapping.
		if strings.Contains(component, "/") || strings.Contains(component, "_") {
			panic(fmt.Errorf("invalid character in component [%q] of CgroupName", component))
		}
	}
	// copy data from the base cgroup to eliminate cases where CgroupNames share underlying slices.  See #68416
	baseCopy := make([]string, len(base))
	copy(baseCopy, base)
	return CgroupName(append(baseCopy, components...))
}

// NodeAllocatableRoot returns the literal cgroup path for the node allocatable cgroup
func NodeAllocatableRoot(cgroupRoot, cgroupDriver string) string {
	root := ParseCgroupfsToCgroupName(cgroupRoot)
	nodeAllocatableRoot := NewCgroupName(root, defaultNodeAllocatableCgroupName)
	//if libcontainerCgroupManagerType(cgroupDriver) == libcontainerSystemd {
	//	return nodeAllocatableRoot.ToSystemd()
	//}
	return nodeAllocatableRoot.ToCgroupfs()
}

// GetKubeletContainer returns the cgroup the kubelet will use
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

// GetRuntimeContainer returns the cgroup used by the container runtime
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

// GetCgroupSubsystems returns information about the mounted cgroup subsystems.
func GetCgroupSubsystems() (*CgroupSubsystems, error) {
	// get all cgroup mounts.
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
	fmt.Printf("======>allCgroups: %v, MountPoints: %v\n", allCgroups, mountPoints)
	return &CgroupSubsystems{
		Mounts:      allCgroups,
		MountPoints: mountPoints,
	}, nil
}



















































