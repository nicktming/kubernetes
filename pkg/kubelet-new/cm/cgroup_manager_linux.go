package cm

import (
	libcontainercgroups "github.com/opencontainers/runc/libcontainer/cgroups"
	libcontainerconfigs "github.com/opencontainers/runc/libcontainer/configs"
	cgroupfs "github.com/opencontainers/runc/libcontainer/cgroups/fs"
	"strings"
	"path"
	"fmt"
	"k8s.io/klog"
	"k8s.io/apimachinery/pkg/util/sets"
	"path/filepath"
	"os"
)

// libcontainerCgroupManagerType defines how to interface with libcontainer
type libcontainerCgroupManagerType string

const (
	// libcontainerCgroupfs means use libcontainer with cgroupfs
	libcontainerCgroupfs libcontainerCgroupManagerType = "cgroupfs"
	// libcontainerSystemd means use libcontainer with systemd
	libcontainerSystemd libcontainerCgroupManagerType = "systemd"
	// systemdSuffix is the cgroup name suffix for systemd
	systemdSuffix string = ".slice"
)

// CgroupSubsystems holds information about the mounted cgroup subsystems
type CgroupSubsystems struct {
	// Cgroup subsystem mounts.
	// e.g.: "/sys/fs/cgroup/cpu" -> ["cpu", "cpuacct"]
	Mounts []libcontainercgroups.Mount

	// Cgroup subsystem to their mount location.
	// e.g.: "cpu" -> "/sys/fs/cgroup/cpu"
	MountPoints map[string]string
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

// libcontainerAdapter provides a simplified interface to libcontainer based on libcontainer type.
type libcontainerAdapter struct {
	// cgroupManagerType defines how to interface with libcontainer
	cgroupManagerType libcontainerCgroupManagerType
}

// newLibcontainerAdapter returns a configured libcontainerAdapter for specified manager.
// it does any initialization required by that manager to function.
func newLibcontainerAdapter(cgroupManagerType libcontainerCgroupManagerType) *libcontainerAdapter {
	return &libcontainerAdapter{cgroupManagerType: cgroupManagerType}
}

// newManager returns an implementation of cgroups.Manager
func (l *libcontainerAdapter) newManager(cgroups *libcontainerconfigs.Cgroup, paths map[string]string) (libcontainercgroups.Manager, error) {
	switch l.cgroupManagerType {
	case libcontainerCgroupfs:
		return &cgroupfs.Manager{
			Cgroups: cgroups,
			Paths:   paths,
		}, nil
	//case libcontainerSystemd:
	//	// this means you asked systemd to manage cgroups, but systemd was not on the host, so all you can do is panic...
	//	if !cgroupsystemd.UseSystemd() {
	//		panic("systemd cgroup manager not available")
	//	}
	//	return &cgroupsystemd.Manager{
	//		Cgroups: cgroups,
	//		Paths:   paths,
	//	}, nil
	}
	return nil, fmt.Errorf("invalid cgroup manager configuration")
}


type cgroupManagerImpl struct {
	// subsystems holds information about all the mounted cgroup subsystems on the node.
	subsystems *CgroupSubsystems

	// simplifies interaction with libcontainer and its cgroup managers
	adapter *libcontainerAdapter
}

func NewCgroupManager(cs *CgroupSubsystems, cgroupDriver string) CgroupManager {
	managerType := libcontainerCgroupfs
	if cgroupDriver == string(libcontainerSystemd) {
		managerType = libcontainerSystemd
	}
	return &cgroupManagerImpl{
		subsystems: cs,
		adapter:    newLibcontainerAdapter(managerType),
	}
}


// Create creates the specified cgroup
func (m *cgroupManagerImpl) Create(cgroupConfig *CgroupConfig) error {
	resources := m.toResources(cgroupConfig.ResourceParameters)
	libcontainerCgroupConfig := &libcontainerconfigs.Cgroup{
		Resources: resources,
	}
	libcontainerCgroupConfig.Path = cgroupConfig.Name.ToCgroupfs()
	// TODO pid limit
	// get the manager with the specified cgroup configuration
	manager, err := m.adapter.newManager(libcontainerCgroupConfig, nil)
	if err != nil {
		return err
	}
	// Apply(-1) is a hack to create the cgroup directories for each resource
	// subsystem. The function [cgroups.Manager.apply()] applies cgroup
	// configuration to the process with the specified pid.
	// It creates cgroup files for each subsystems and writes the pid
	// in the tasks file. We use the function to create all the required
	// cgroup files but not attach any "real" pid to the cgroup.
	if err := manager.Apply(-1); err != nil {
		return err
	}

	// TODO update

	if err := m.Update(cgroupConfig); err != nil {
		return err
	}
	return nil
}

func (m *cgroupManagerImpl) toResources(resourceConfig *ResourceConfig) *libcontainerconfigs.Resources {
	resources := &libcontainerconfigs.Resources{}
	if resourceConfig == nil {
		return resources
	}
	if resourceConfig.Memory != nil {
		resources.Memory = *resourceConfig.Memory
	}
	// TODO cpu quota huge page limit
	return resources
}

func (m *cgroupManagerImpl) Name(name CgroupName) string {
	// TODO systemd
	return name.ToCgroupfs()
}

type subsystem interface {
	// Name returns the name of the subsystem.
	Name() string
	// Set the cgroup represented by cgroup.
	Set(path string, cgroup *libcontainerconfigs.Cgroup) error
	// GetStats returns the statistics associated with the cgroup
	GetStats(path string, stats *libcontainercgroups.Stats) error
}

// TODO

func getSupportedSubsystems() map[subsystem]bool {
	supportedSubsystems := map[subsystem]bool {
		&cgroupfs.MemoryGroup{}: true,
		&cgroupfs.CpuGroup{}:    true,
		&cgroupfs.PidsGroup{}:   false,
	}
	// TODO huge
	return supportedSubsystems
}

func setSupportedSubsystems(cgroupConfig *libcontainerconfigs.Cgroup) error {
	for sys, required := range getSupportedSubsystems() {
		if _, ok := cgroupConfig.Paths[sys.Name()]; !ok {
			if required {
				return fmt.Errorf("Failed to find subsystem mount for required subsystem: %v", sys.Name())
			}
			// the cgroup is not mounted, but its not required so continue...
			klog.Infof("Unable to find subsystem mount for optional subsystem: %v", sys.Name())
			continue
		}
		if err := sys.Set(cgroupConfig.Paths[sys.Name()], cgroupConfig); err != nil {
			return fmt.Errorf("Failed to set config for supported subsystems : %v", err)
		}
	}
	return nil
}

func (m *cgroupManagerImpl) buildCgroupPaths(name CgroupName) map[string]string {
	cgroupFsAdapterName := m.Name(name)
	cgroupPaths := make(map[string]string, len(m.subsystems.MountPoints))
	for key, val := range m.subsystems.MountPoints {
		cgroupPaths[key] = path.Join(val, cgroupFsAdapterName)
	}
	return cgroupPaths
}

// Update updates the cgroup with the specified Cgroup Configuration
func (m *cgroupManagerImpl) Update(cgroupConfig *CgroupConfig) error {

	// Extract the cgroup resource parameters
	resourceConfig := cgroupConfig.ResourceParameters
	resources := m.toResources(resourceConfig)

	cgroupPaths := m.buildCgroupPaths(cgroupConfig.Name)

	libcontainerCgroupConfig := &libcontainerconfigs.Cgroup {
		Resources: 	resources,
		Paths: 		cgroupPaths,
	}
	libcontainerCgroupConfig.Path = cgroupConfig.Name.ToCgroupfs()

	// TODO pid limit

	if err := setSupportedSubsystems(libcontainerCgroupConfig); err != nil {
		return err
	}

	return nil
}

func (m *cgroupManagerImpl) Destroy(cgroupConfig *CgroupConfig) error {
	libcontainerCgroupConfig := &libcontainerconfigs.Cgroup{
		Path:   cgroupConfig.Name.ToCgroupfs(),
	}
	cgroupPaths := m.buildCgroupPaths(cgroupConfig.Name)
	manager, err := m.adapter.newManager(libcontainerCgroupConfig, cgroupPaths)
	if err != nil {
		return err
	}
	if err := manager.Destroy(); err != nil {
		return err
	}
	return nil
}

func (m *cgroupManagerImpl) Exists(name CgroupName) bool {
	cgroupPaths := m.buildCgroupPaths(name)

	whitelistControllers := sets.NewString("cpu", "cpuacct", "cpuset", "memory", "systemd")
	//if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.SupportPodPidsLimit) || utilfeature.DefaultFeatureGate.Enabled(kubefeatures.SupportNodePidsLimit) {
	//	whitelistControllers.Insert("pids")
	//}
	var missingPaths []string
	for controller, path := range cgroupPaths {
		if !whitelistControllers.Has(controller) {
			continue
		}
		if !libcontainercgroups.PathExists(path) {
			missingPaths = append(missingPaths, path)
		}
	}
	if len(missingPaths) > 0 {
		klog.V(4).Infof("The Cgroup %v has some missing paths: %v", name, missingPaths)
		return false
	}
	return true
}

// CgroupName converts the literal cgroupfs name on the host to an internal identifier.
func (m *cgroupManagerImpl) CgroupName(name string) CgroupName {
	//if m.adapter.cgroupManagerType == libcontainerSystemd {
	//	return ParseSystemdToCgroupName(name)
	//}
	return ParseCgroupfsToCgroupName(name)
}


// Scans through all subsystems to find pids associated with specified cgroup.
func (m *cgroupManagerImpl) Pids(name CgroupName) []int {
	// we need the driver specific name
	cgroupFsName := m.Name(name)

	// Get a list of processes that we need to kill
	pidsToKill := sets.NewInt()
	var pids []int
	for _, val := range m.subsystems.MountPoints {
		dir := path.Join(val, cgroupFsName)
		_, err := os.Stat(dir)
		if os.IsNotExist(err) {
			// The subsystem pod cgroup is already deleted
			// do nothing, continue
			continue
		}
		// Get a list of pids that are still charged to the pod's cgroup
		pids, err = getCgroupProcs(dir)
		if err != nil {
			continue
		}
		pidsToKill.Insert(pids...)

		// WalkFunc which is called for each file and directory in the pod cgroup dir
		visitor := func(path string, info os.FileInfo, err error) error {
			if err != nil {
				klog.V(4).Infof("cgroup manager encountered error scanning cgroup path %q: %v", path, err)
				return filepath.SkipDir
			}
			if !info.IsDir() {
				return nil
			}
			pids, err = getCgroupProcs(path)
			if err != nil {
				klog.V(4).Infof("cgroup manager encountered error getting procs for cgroup path %q: %v", path, err)
				return filepath.SkipDir
			}
			pidsToKill.Insert(pids...)
			return nil
		}
		// Walk through the pod cgroup directory to check if
		// container cgroups haven't been GCed yet. Get attached processes to
		// all such unwanted containers under the pod cgroup
		if err = filepath.Walk(dir, visitor); err != nil {
			klog.V(4).Infof("cgroup manager encountered error scanning pids for directory: %q: %v", dir, err)
		}
	}
	return pidsToKill.List()
}


func (m *cgroupManagerImpl) ReduceCPULimits(cgroupName CgroupName) error {
	minimumCPUShares := uint64(MinShares)
	cgroupConfig := &CgroupConfig {
		Name: cgroupName,
		ResourceParameters: &ResourceConfig{
			CpuShares: &minimumCPUShares,
		},
	}
	return m.Update(cgroupConfig)
}


func getStatsSupportedSubsystems(cgroupPaths map[string]string) (*libcontainercgroups.Stats, error) {
	stats := libcontainercgroups.NewStats()
	for sys, required := range getSupportedSubsystems() {
		if _, ok := cgroupPaths[sys.Name()]; !ok {
			if required {
				return nil, fmt.Errorf("Failed to find subsystem mount for required subsystem: %v", sys.Name())
			}
			// the cgroup is not mounted, but its not required so continue...
			klog.V(6).Infof("Unable to find subsystem mount for optional subsystem: %v", sys.Name())
			continue
		}
		if err := sys.GetStats(cgroupPaths[sys.Name()], stats); err != nil {
			return nil, fmt.Errorf("Failed to get stats for supported subsystems : %v", err)
		}
	}
	return stats, nil
}

func toResourceStats(stats *libcontainercgroups.Stats) *ResourceStats {
	return &ResourceStats{
		MemoryStats: &MemoryStats{
			Usage: int64(stats.MemoryStats.Usage.Usage),
		},
	}
}

// Get sets the ResourceParameters of the specified cgroup as read from the cgroup fs
func (m *cgroupManagerImpl) GetResourceStats(name CgroupName) (*ResourceStats, error) {
	cgroupPaths := m.buildCgroupPaths(name)
	stats, err := getStatsSupportedSubsystems(cgroupPaths)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats supported cgroup subsystems for cgroup %v: %v", name, err)
	}
	return toResourceStats(stats), nil
}




























































































