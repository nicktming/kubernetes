package cm

import (
	"fmt"

	"github.com/opencontainers/runc/libcontainer/cgroups"
	"github.com/opencontainers/runc/libcontainer/cgroups/fs"

	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/util/procfs"

	"os"
	"io/ioutil"
	"strconv"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/kubelet-tming/cadvisor"
	"k8s.io/client-go/tools/record"
	"strings"
	"bytes"
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/cm/devicemanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager"
	"sync"
	"k8s.io/kubernetes/pkg/kubelet-tming/config"
	"k8s.io/kubernetes/pkg/kubelet-tming/status"
	internalapi "k8s.io/cri-api/pkg/apis"

	kubefeatures "k8s.io/kubernetes/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"k8s.io/apimachinery/pkg/util/wait"
	"time"

	//"k8s.io/apimachinery/pkg/api/resource"
)

const (
	dockerProcessName	= "docker"
	dockerPidFile		= "/var/run/docker.pid"

)


type systemContainer struct {
	name 			string
	cpuMillicores		int64

	ensureStateFunc 	func(m *fs.Manager) error
	manager 		*fs.Manager

}

type containerManagerImpl struct {
	sync.RWMutex
	cadvisorInterface cadvisor.Interface
	mountUtil         mount.Interface
	NodeConfig
	status Status
	// External containers being managed.
	systemContainers []*systemContainer
	//qosContainers    QOSContainersInfo
	// Tasks that are run periodically
	periodicTasks []func()
	// Holds all the mounted cgroup subsystems
	subsystems *CgroupSubsystems
	nodeInfo   *v1.Node
	// Interface for cgroup management
	cgroupManager CgroupManager
	// Capacity of this node.
	capacity v1.ResourceList
	// Capacity of this node, including internal resources.
	internalCapacity v1.ResourceList
	// Absolute cgroupfs path to a cgroup that Kubelet needs to place all pods under.
	// This path include a top level container for enforcing Node Allocatable.
	cgroupRoot CgroupName
	// Event recorder interface.
	recorder record.EventRecorder
	// Interface for QoS cgroup management
	//qosContainerManager QOSContainerManager
	// Interface for exporting and allocating devices reported by device plugins.
	deviceManager devicemanager.Manager
	// Interface for CPU affinity management.
	cpuManager cpumanager.Manager
}



func NewContainerManager(mountUtil mount.Interface, cadvisorInterface cadvisor.Interface, nodeConfig NodeConfig, failSwapOn bool, devicePluginEnabled bool, recorder record.EventRecorder) (ContainerManager, error) {
	for i, pageSize := range fs.HugePageSizes {
		fs.HugePageSizes[i] = strings.ReplaceAll(pageSize, "kB", "KB")
	}
	subsystems, err := GetCgroupSubsystems()
	if err != nil {
		return nil, fmt.Errorf("failed to get mounted cgroup subsystems: %v", err)
	}
	if failSwapOn {

		// TODO why

		// Check whether swap is enabled. The Kubelet does not support running with swap enabled.
		swapData, err := ioutil.ReadFile("/proc/swaps")
		if err != nil {
			return nil, err
		}
		swapData = bytes.TrimSpace(swapData) // extra trailing \n
		swapLines := strings.Split(string(swapData), "\n")

		// If there is more than one line (table headers) in /proc/swaps, swap is enabled and we should
		// error out unless --fail-swap-on is set to false.
		if len(swapLines) > 1 {
			return nil, fmt.Errorf("Running with swap on is not supported, please disable swap! or set --fail-swap-on flag to false. /proc/swaps contained: %v", swapLines)
		}
	}
	var capacity = v1.ResourceList{}
	var internalCapacity = v1.ResourceList{}
	machineInfo, err := cadvisorInterface.MachineInfo()
	if err != nil {
		return nil, err
	}
	capacity = cadvisor.CapacityFromMachineInfo(machineInfo)
	for k, v := range capacity {
		internalCapacity[k] = v
	}

	// TODO
	//pidlimits, err := pidlimit.Stats()
	//if err == nil && pidlimits != nil && pidlimits.MaxPID != nil {
	//	internalCapacity[pidlimit.PIDs] = *resource.NewQuantity(
	//		int64(*pidlimits.MaxPID),
	//		resource.DecimalSI)
	//}

	// Turn CgroupRoot from a string (in cgroupfs path format) to internal CgroupName
	cgroupRoot := ParseCgroupfsToCgroupName(nodeConfig.CgroupRoot)
	cgroupManager := NewCgroupManager(subsystems, nodeConfig.CgroupDriver)
	// Check if Cgroup-root actually exists on the node
	//if nodeConfig.CgroupsPerQOS {
	//	// this does default to / when enabled, but this tests against regressions.
	//	if nodeConfig.CgroupRoot == "" {
	//		return nil, fmt.Errorf("invalid configuration: cgroups-per-qos was specified and cgroup-root was not specified. To enable the QoS cgroup hierarchy you need to specify a valid cgroup-root")
	//	}
	//
	//	// we need to check that the cgroup root actually exists for each subsystem
	//	// of note, we always use the cgroupfs driver when performing this check since
	//	// the input is provided in that format.
	//	// this is important because we do not want any name conversion to occur.
	//	if !cgroupManager.Exists(cgroupRoot) {
	//		return nil, fmt.Errorf("invalid configuration: cgroup-root %q doesn't exist", cgroupRoot)
	//	}
	//	klog.Infof("container manager verified user specified cgroup-root exists: %v", cgroupRoot)
	//	// Include the top level cgroup for enforcing node allocatable into cgroup-root.
	//	// This way, all sub modules can avoid having to understand the concept of node allocatable.
	//	cgroupRoot = NewCgroupName(cgroupRoot, defaultNodeAllocatableCgroupName)
	//}
	klog.Infof("Creating Container Manager object based on Node Config: %+v", nodeConfig)

	// TODO QOS
	//qosContainerManager, err := NewQOSContainerManager(subsystems, cgroupRoot, nodeConfig, cgroupManager)
	//if err != nil {
	//	return nil, err
	//}


	cm := &containerManagerImpl{
		cadvisorInterface:   cadvisorInterface,
		mountUtil:           mountUtil,
		NodeConfig:          nodeConfig,
		subsystems:          subsystems,
		cgroupManager:       cgroupManager,
		capacity:            capacity,
		internalCapacity:    internalCapacity,
		cgroupRoot:          cgroupRoot,
		recorder:            recorder,
		//qosContainerManager: qosContainerManager,
	}

	// TODO deviceplugin
	// TODO CPUManager

	return cm, nil
}

func (cm *containerManagerImpl) Start(node *v1.Node,
			activePods ActivePodsFunc,
			sourcesReady config.SourcesReady,
			podStatusProvider status.PodStatusProvider,
			runtimeService internalapi.RuntimeService) error {

	// TODO
	// Initialize CPU manager
	//if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.CPUManager) {
	//	cm.cpuManager.Start(cpumanager.ActivePodsFunc(activePods), podStatusProvider, runtimeService)
	//}

	cm.nodeInfo = node


	// TODO
	if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.LocalStorageCapacityIsolation) {
		rootfs, err := cm.cadvisorInterface.RootFsInfo()
		if err != nil {
			return fmt.Errorf("failed to get rootfs info: %v", err)
		}
		for rName, rCap := range cadvisor.EphemeralStorageCapacityFromFsInfo(rootfs) {
			cm.capacity[rName] = rCap
		}
	}

	// TODO
	// Ensure that node allocatable configuration is valid.
	//if err := cm.validateNodeAllocatable(); err != nil {
	//	return err
	//}

	// TODO
	// Setup the node
	//if err := cm.setupNode(activePods); err != nil {
	//	return err
	//}

	// Don't run a background thread if there are no ensureStateFuncs.
	hasEnsureStateFuncs := false
	for _, cont := range cm.systemContainers {
		if cont.ensureStateFunc != nil {
			hasEnsureStateFuncs = true
			break
		}
	}
	if hasEnsureStateFuncs {
		// Run ensure state functions every minute.
		go wait.Until(func() {
			for _, cont := range cm.systemContainers {
				if cont.ensureStateFunc != nil {
					if err := cont.ensureStateFunc(cont.manager); err != nil {
						klog.Warningf("[ContainerManager] Failed to ensure state of %q: %v", cont.name, err)
					}
				}
			}
		}, time.Minute, wait.NeverStop)

	}

	if len(cm.periodicTasks) > 0 {
		go wait.Until(func() {
			for _, task := range cm.periodicTasks {
				if task != nil {
					task()
				}
			}
		}, 5*time.Minute, wait.NeverStop)
	}

	// TODO
	// Starts device manager.
	//if err := cm.deviceManager.Start(devicemanager.ActivePodsFunc(activePods), sourcesReady); err != nil {
	//	return err
	//}

	return nil
}


func (cm *containerManagerImpl) GetCapacity() v1.ResourceList {
	return cm.capacity
}

func (cm *containerManagerImpl) GetDevicePluginResourceCapacity() (v1.ResourceList, v1.ResourceList, []string) {
	return cm.deviceManager.GetCapacity()
}

//func (cm *containerManagerImpl) GetNodeAllocatableReservation() v1.ResourceList {
//	evictionReservation := hardEvictionReservation(cm.HardEvictionThresholds, cm.capacity)
//	result := make(v1.ResourceList)
//	for k := range cm.capacity {
//		value := resource.NewQuantity(0, resource.DecimalSI)
//		if cm.NodeConfig.SystemReserved != nil {
//			value.Add(cm.NodeConfig.SystemReserved[k])
//		}
//		if cm.NodeConfig.KubeReserved != nil {
//			value.Add(cm.NodeConfig.KubeReserved[k])
//		}
//		if evictionReservation != nil {
//			value.Add(evictionReservation[k])
//		}
//		if !value.IsZero() {
//			result[k] = *value
//		}
//	}
//	return result
//}
//
//
//


func (cm *containerManagerImpl) GetNodeConfig() NodeConfig {
	cm.RLock()
	defer cm.RUnlock()
	return cm.NodeConfig
}








































































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

