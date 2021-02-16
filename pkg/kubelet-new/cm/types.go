package cm


// ResourceConfig holds information about all the supported cgroup resource parameters.
type ResourceConfig struct {
	// Memory limit (in bytes)
	Memory *int64
	// CPU shares (relative weight vs. other containers).
	CpuShares *uint64
	// CPU hardcap limit(in usecs). Allowed cpu time in a given period.
	//CpuQuota *int64
}

type CgroupConfig struct {
	// Fully qualified name prior to any driver specific conversions.
	Name CgroupName
	// ResourceParameters containers various cgroups settings to apply.
	ResourceParameters *ResourceConfig
}

// MemoryStats holds the on-demand statistics from the memory cgroup
type MemoryStats struct {
	// Memory usage (in bytes).
	Usage int64
}

// ResourceStats holds on-demand statistics from various cgroup subsystems
type ResourceStats struct {
	// Memory statistics.
	MemoryStats *MemoryStats
}

// CgroupName is the abstract name of a cgroup prior to any driver specific conversion.
// It is specified as a list of strings from its individual components, such as:
// {"kubepods", "burstable", "pod1234-abcd-5678-efgh"}
type CgroupName []string


type CgroupManager interface {
	// Create creates and applies the cgroup configurations on the cgroup.
	// It just creates the leaf cgroups.
	// It expects the parent cgroup to already exist.
	Create(*CgroupConfig) error

	// Destroy the cgroup.
	Destroy(*CgroupConfig) error
	// Update cgroup configuration.
	Update(*CgroupConfig) error
}

