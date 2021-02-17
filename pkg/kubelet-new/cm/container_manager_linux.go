package cm

import (
	"k8s.io/client-go/tools/record"
	"github.com/opencontainers/runc/libcontainer/cgroups"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/util/procfs"
	"k8s.io/api/core/v1"
	"fmt"
	"os"
	"io/ioutil"
	"strconv"
	"encoding/json"
	"k8s.io/kubernetes/pkg/kubelet-new/status"
	internalapi "k8s.io/cri-api/pkg/apis"
	"k8s.io/kubernetes/pkg/kubelet-new/config"
)

const (
	dockerProcessName     = "docker"
	dockerPidFile         = "/var/run/docker.pid"
)

type containerManagerImpl struct {
	// Holds all the mounted cgroup subsystems
	subsystems *CgroupSubsystems

	nodeInfo   *v1.Node

	NodeConfig

	// Absolute cgroupfs path to a cgroup that Kubelet needs to place all pods under.
	// This path include a top level container for enforcing Node Allocatable.
	cgroupRoot CgroupName
}

// TODO(vmarmol): Add limits to the system containers.
// Takes the absolute name of the specified containers.
// Empty container name disables use of the specified container.

// TODO mountUtil mount.Interface, cadvisorInterface cadvisor.Interface
func NewContainerManager(nodeConfig NodeConfig, failSwapOn bool, devicePluginEnabled bool, recorder record.EventRecorder) (ContainerManager, error) {
	// Mitigation of the issue fixed in master where hugetlb prefix for page sizes with "KiB"
	// is "kB" in runc, but the correct is "KB"
	// See https://github.com/opencontainers/runc/pull/2065
	// and https://github.com/kubernetes/kubernetes/pull/78495
	// for more info.
	subsystems, err := GetCgroupSubsystems()
	if err != nil {
		return nil, fmt.Errorf("failed to get mounted cgroup subsystems: %v", err)
	}
	// TODO failswapon

	// TODO pid limit

	// Turn CgroupRoot from a string (in cgroupfs path format) to internal CgroupName
	cgroupRoot := ParseCgroupfsToCgroupName(nodeConfig.CgroupRoot)
	cgroupManager := NewCgroupManager(subsystems, nodeConfig.CgroupDriver)

	if nodeConfig.CgroupsPerQOS {
		// this does default to / when enabled, but this tests against regressions.
		if nodeConfig.CgroupRoot == "" {
			return nil, fmt.Errorf("invalid configuration: cgroups-per-qos was specified and cgroup-root was not specified. To enable the QoS cgroup hierarchy you need to specify a valid cgroup-root")
		}

		// we need to check that the cgroup root actually exists for each subsystem
		// of note, we always use the cgroupfs driver when performing this check since
		// the input is provided in that format.
		// this is important because we do not want any name conversion to occur.
		if !cgroupManager.Exists(cgroupRoot) {
			return nil, fmt.Errorf("invalid configuration: cgroup-root %q doesn't exist", cgroupRoot)
		}
		klog.Infof("container manager verified user specified cgroup-root exists: %v", cgroupRoot)
		// Include the top level cgroup for enforcing node allocatable into cgroup-root.
		// This way, all sub modules can avoid having to understand the concept of node allocatable.
		cgroupRoot = NewCgroupName(cgroupRoot, defaultNodeAllocatableCgroupName)
	}

	pretty_nodeconfig, _ := json.MarshalIndent(nodeConfig, "", "\t")
	klog.Infof("Creating Container Manager object based on Node Config: %v\n cgroupRoot: %v",
		string(pretty_nodeconfig), cgroupRoot)

	cm := &containerManagerImpl{
		subsystems: 		subsystems,
		NodeConfig:		nodeConfig,
		cgroupRoot: 		cgroupRoot,
	}

	return cm, nil
}

// getContainer returns the cgroup associated with the specified pid.
// It enforces a unified hierarchy for memory and cpu cgroups.
// On systemd environments, it uses the name=systemd cgroup for the specified pid.
func getContainer(pid int) (string, error) {
	cgs, err := cgroups.ParseCgroupFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return "", err
	}
	cpu, found := cgs["cpu"]
	if !found {
		return "", cgroups.NewNotFoundError("cpu")
	}
	memory, found := cgs["memory"]
	if !found {
		return "", cgroups.NewNotFoundError("memory")
	}

	// since we use this container for accounting, we need to ensure its a unified hierarchy.
	if cpu != memory {
		return "", fmt.Errorf("cpu and memory cgroup hierarchy not unified.  cpu: %s, memory: %s", cpu, memory)
	}

	// on systemd, every pid is in a unified cgroup hierarchy (name=systemd as seen in systemd-cgls)
	// cpu and memory accounting is off by default, users may choose to enable it per unit or globally.
	// users could enable CPU and memory accounting globally via /etc/systemd/system.conf (DefaultCPUAccounting=true DefaultMemoryAccounting=true).
	// users could also enable CPU and memory accounting per unit via CPUAccounting=true and MemoryAccounting=true
	// we only warn if accounting is not enabled for CPU or memory so as to not break local development flows where kubelet is launched in a terminal.
	// for example, the cgroup for the user session will be something like /user.slice/user-X.slice/session-X.scope, but the cpu and memory
	// cgroup will be the closest ancestor where accounting is performed (most likely /) on systems that launch docker containers.
	// as a result, on those systems, you will not get cpu or memory accounting statistics for kubelet.
	// in addition, you would not get memory or cpu accounting for the runtime unless accounting was enabled on its unit (or globally).
	if systemd, found := cgs["name=systemd"]; found {
		if systemd != cpu {
			klog.Warningf("CPUAccounting not enabled for pid: %d", pid)
		}
		if systemd != memory {
			klog.Warningf("MemoryAccounting not enabled for pid: %d", pid)
		}
		return systemd, nil
	}

	return cpu, nil

}

func getPidFromPidFile(pidFile string) (int, error) {
	file, err := os.Open(pidFile)
	if err != nil {
		return 0, fmt.Errorf("error opening pid file %s: %v", pidFile, err)
	}
	defer file.Close()

	data, err := ioutil.ReadAll(file)
	if err != nil {
		return 0, fmt.Errorf("error reading pid file %s: %v", pidFile, err)
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, fmt.Errorf("error parsing %s as a number: %v", string(data), err)
	}

	return pid, nil
}

func getPidsForProcess(name, pidFile string) ([]int, error) {
	if len(pidFile) == 0 {
		return procfs.PidOf(name)
	}

	pid, err := getPidFromPidFile(pidFile)
	if err == nil {
		return []int{pid}, nil
	}

	// Try to lookup pid by process name
	pids, err2 := procfs.PidOf(name)
	if err2 == nil {
		return pids, nil
	}

	// Return error from getPidFromPidFile since that should have worked
	// and is the real source of the problem.
	klog.V(4).Infof("unable to get pid from %s: %v", pidFile, err)
	return []int{}, err
}

func getContainerNameForProcess(name, pidFile string) (string, error) {
	pids, err := getPidsForProcess(name, pidFile)
	if err != nil {
		return "", fmt.Errorf("failed to detect process id for %q - %v", name, err)
	}
	if len(pids) == 0 {
		return "", nil
	}
	cont, err := getContainer(pids[0])
	if err != nil {
		return "", err
	}
	return cont, nil
}

func (cm *containerManagerImpl) setupNode(activePods ActivePodsFunc) error {
	// TODO validateSystemRequirements
	// TODO ProtectKernelDefaults

	return nil
}

func (cm *containerManagerImpl) Start(node *v1.Node,
	activePods ActivePodsFunc,
	sourcesReady config.SourcesReady,
	podStatusProvider status.PodStatusProvider,
	runtimeService internalapi.RuntimeService) error {

	// TODO Initialize CPU manager

	cm.nodeInfo = node

	// TODO LocalStorageCapacityIsolation
	// TODO validateNodeAllocatable

	// Setup the node
	if err := cm.setupNode(activePods); err != nil {
		return err
	}

	// Enforce Node Allocatable (if required)
	if err := cm.enforceNodeAllocatableCgroups(); err != nil {
		return err
	}

	return nil
}






















































