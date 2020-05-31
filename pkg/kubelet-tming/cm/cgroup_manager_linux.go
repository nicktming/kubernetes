package cm

import (
	"strings"
	"path"
	"fmt"
	cgroupsystemd "github.com/opencontainers/runc/libcontainer/cgroups/systemd"
	libcontainercgroups "github.com/opencontainers/runc/libcontainer/cgroups"
)

type libcontainerCgroupManagerType string

const (
	libcontainerCgroupfs 	libcontainerCgroupManagerType = "cgroupfs"
	libcontainerSystemd 	libcontainerCgroupManagerType = "systemd"
	systemdSuffix	 	string 			      = ".slice"
)

type CgroupSubsystems struct {
	Mounts 		[]libcontainercgroups.Mount

	MountPoints 	map[string]string
}

type cgroupManagerImpl struct {
	subsystems 	*CgroupSubsystems

	//adapter 	*libcontainerAdapter
}

func NewCgroupManager(cs *CgroupSubsystems, cgroupDriver string) CgroupManager {
	//managerType := libcontainerCgroupfs
	//if cgroupDriver == string(libcontainerSystemd) {
	//	managerType = libcontainerSystemd
	//}
	return &cgroupManagerImpl{
		subsystems: 	cs,
		//adapter:
	}
}

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

func ParseSystemdToCgroupName(name string) CgroupName {
	driverName := path.Base(name)
	driverName = strings.TrimSuffix(driverName, systemdSuffix)
	parts := strings.Split(driverName, "-")
	result := []string{}
	for _, part := range parts {
		result = append(result, unescapeSystemdCgroupName(part))
	}
	return CgroupName(result)
}

func escapeSystemdCgroupName(part string) string {
	return strings.Replace(part, "-", "_", -1)
}

func unescapeSystemdCgroupName(part string) string {
	return strings.Replace(part, "_", "-", -1)
}


func ParseCgroupfsToCgroupName(name string) CgroupName {
	components := strings.Split(strings.TrimPrefix(name, "/"), "/")
	if len(components) == 1 && components[0] == "" {
		components = []string{}
	}
	return CgroupName(components)
 }

func (cgroupName CgroupName) ToCgroupfs() string {
	return "/" + path.Join(cgroupName...)
}

func (cgroupName CgroupName) ToSystemd() string {
	if len(cgroupName) == 0 || (len(cgroupName) == 1 && cgroupName[0] == "") {
		return "/"
	}
	newparts := []string{}
	for _, part := range cgroupName {
		part = escapeSystemdCgroupName(part)
		newparts = append(newparts, part)
	}

	result, err := cgroupsystemd.ExpandSlice(strings.Join(newparts, "-") + systemdSuffix)
	if err != nil {
		panic(fmt.Errorf("error converting cgroup name [%v] to systemd format: %v", cgroupName, err))
	}
	return result
}























































