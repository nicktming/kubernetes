package cm

import (

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	v1qos "k8s.io/kubernetes/pkg/apis/core/v1/helper/qos"
	"k8s.io/klog"
	"fmt"
)

const (
	podCgroupNamePrefix = "pod"
)

// podContainerManagerImpl implements podContainerManager interface.
// It is the general implementation which allows pod level container
// management if qos Cgroup is enabled.
type podContainerManagerImpl struct {
	// qosContainersInfo hold absolute paths of the top level qos containers
	qosContainersInfo QOSContainersInfo
	// Stores the mounted cgroup subsystems
	subsystems *CgroupSubsystems
	// cgroupManager is the cgroup Manager Object responsible for managing all
	// pod cgroups.
	cgroupManager CgroupManager
	// Maximum number of pids in a pod
	podPidsLimit int64
	// enforceCPULimits controls whether cfs quota is enforced or not
	enforceCPULimits bool
	// cpuCFSQuotaPeriod is the cfs period value, cfs_period_us, setting per
	// node for all containers in usec
	cpuCFSQuotaPeriod uint64
}


func (m *podContainerManagerImpl) Exists(pod *v1.Pod) bool {
	podContainerName, _ := m.GetPodContainerName(pod)
	return m.cgroupManager.Exists(podContainerName)
}

func (m *podContainerManagerImpl) EnsureExists(pod *v1.Pod) error {
	podContainerName, _ := m.GetPodContainerName(pod)
	// check if container already exist
	alreadyExists := m.Exists(pod)
	if !alreadyExists {
		// Create the pod container
		containerConfig := &CgroupConfig {
			Name: 				podContainerName,
			ResourceParameters:		ResourceConfigForPod(pod, m.enforceCPULimits, m.cpuCFSQuotaPeriod),
		}
		//if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.SupportPodPidsLimit) && m.podPidsLimit > 0 {
		//	containerConfig.ResourceParameters.PidsLimit = &m.podPidsLimit
		//}
		if err := m.cgroupManager.Create(containerConfig); err != nil {
			return fmt.Errorf("failed to create container for %v : %v", podContainerName, err)
		}

	}
	// Apply appropriate resource limits on the pod container
	// Top level qos containers limits are not updated
	// until we figure how to maintain the desired state in the kubelet.
	// Because maintaining the desired state is difficult without checkpointing.
	if err := m.applyLimits(pod); err != nil {
		return fmt.Errorf("failed to apply resource limits on container for %v : %v", podContainerName, err)
	}
	return nil

}

// applyLimits sets pod cgroup resource limits
// It also updates the resource limits on top level qos containers.
func (m *podContainerManagerImpl) applyLimits(pod *v1.Pod) error {
	// This function will house the logic for setting the resource parameters
	// on the pod container config and updating top level qos container configs
	return nil
}


// GetPodContainerName returns the CgroupName identifier, and its literal cgroupfs form on the host
func (m *podContainerManagerImpl) GetPodContainerName(pod *v1.Pod) (CgroupName, string) {
	podQOS := v1qos.GetPodQOS(pod)
	// Get the parent QOS container name
	var parentContainer CgroupName
	switch podQOS {
	case v1.PodQOSGuaranteed:
		parentContainer = m.qosContainersInfo.Guaranteed
	case v1.PodQOSBurstable:
		parentContainer = m.qosContainersInfo.Burstable
	case v1.PodQOSBestEffort:
		parentContainer = m.qosContainersInfo.BestEffort
	}

	podContainer := GetPodCgroupNameSuffix(pod.UID)

	// Get the absolute path of the cgroup
	cgroupName := NewCgroupName(parentContainer, podContainer)

	// Get the literal cgroupfs name
	cgroupfsName := m.cgroupManager.Name(cgroupName)

	klog.Infof("GetPodContainerName podQOS: %v, parentContainer: %v, cgroupName: %v, cgroupfsName: %v", podQOS, parentContainer, cgroupName, cgroupfsName)

	return cgroupName, cgroupfsName
}



// podContainerManagerNoop implements podContainerManager interface.
// It is a no-op implementation and basically does nothing
// podContainerManagerNoop is used in case the QoS cgroup Hierarchy is not
// enabled, so Exists() returns true always as the cgroupRoot
// is expected to always exist.
type podContainerManagerNoop struct {
	cgroupRoot CgroupName
}

// Make sure that podContainerManagerStub implements the PodContainerManager interface
var _ PodContainerManager = &podContainerManagerNoop{}

func (m *podContainerManagerNoop) Exists(_ *v1.Pod) bool {
	return true
}

func (m *podContainerManagerNoop) EnsureExists(_ *v1.Pod) error {
	return nil
}

func (m *podContainerManagerNoop) GetPodContainerName(_ *v1.Pod) (CgroupName, string) {
	return m.cgroupRoot, ""
}

func (m *podContainerManagerNoop) GetPodContainerNameForDriver(_ *v1.Pod) string {
	return ""
}

// Destroy destroys the pod container cgroup paths
func (m *podContainerManagerNoop) Destroy(_ CgroupName) error {
	return nil
}

func (m *podContainerManagerNoop) ReduceCPULimits(_ CgroupName) error {
	return nil
}

func (m *podContainerManagerNoop) GetAllPodsFromCgroups() (map[types.UID]CgroupName, error) {
	return nil, nil
}

func (m *podContainerManagerNoop) IsPodCgroup(cgroupfs string) (bool, types.UID) {
	return false, types.UID("")
}
