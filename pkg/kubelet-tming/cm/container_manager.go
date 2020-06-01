package cm

import (
	v1 "k8s.io/api/core/v1"
	"fmt"
	"strings"
	"strconv"
	"k8s.io/apimachinery/pkg/util/sets"
	"time"
	"k8s.io/kubernetes/pkg/kubelet-tming/config"
	"k8s.io/kubernetes/pkg/kubelet-tming/status"
	internalapi "k8s.io/cri-api/pkg/apis"
	evictionapi "k8s.io/kubernetes/pkg/kubelet/eviction/api"
)

type ActivePodsFunc func() []*v1.Pod

type ContainerManager interface {


	Start(*v1.Node, ActivePodsFunc, config.SourcesReady, status.PodStatusProvider, internalapi.RuntimeService) error

	GetCapacity()				v1.ResourceList

	GetDevicePluginResourceCapacity() 	(v1.ResourceList, v1.ResourceList, []string)

	GetNodeAllocatableReservation()		v1.ResourceList

	// GetNodeConfig returns a NodeConfig that is being used by the container manager.
	GetNodeConfig() NodeConfig

}


type NodeConfig struct {
	RuntimeCgroupsName    string
	SystemCgroupsName     string
	KubeletCgroupsName    string
	ContainerRuntime      string
	CgroupsPerQOS         bool
	CgroupRoot            string
	CgroupDriver          string
	KubeletRootDir        string
	ProtectKernelDefaults bool
	NodeAllocatableConfig
	QOSReserved                           map[v1.ResourceName]int64
	ExperimentalCPUManagerPolicy          string
	ExperimentalCPUManagerReconcilePeriod time.Duration
	ExperimentalPodPidsLimit              int64
	EnforceCPULimits                      bool
	CPUCFSQuotaPeriod                     time.Duration
}

type NodeAllocatableConfig struct {
	KubeReservedCgroupName   string
	SystemReservedCgroupName string
	EnforceNodeAllocatable   sets.String
	KubeReserved             v1.ResourceList
	SystemReserved           v1.ResourceList
	HardEvictionThresholds   []evictionapi.Threshold
}

type Status struct {
	// Any soft requirements that were unsatisfied.
	SoftRequirements error
}


// parsePercentage parses the percentage string to numeric value.
func parsePercentage(v string) (int64, error) {
	if !strings.HasSuffix(v, "%") {
		return 0, fmt.Errorf("percentage expected, got '%s'", v)
	}
	percentage, err := strconv.ParseInt(strings.TrimRight(v, "%"), 10, 0)
	if err != nil {
		return 0, fmt.Errorf("invalid number in percentage '%s'", v)
	}
	if percentage < 0 || percentage > 100 {
		return 0, fmt.Errorf("percentage must be between 0 and 100")
	}
	return percentage, nil
}

// ParseQOSReserved parses the --qos-reserve-requests option
func ParseQOSReserved(m map[string]string) (*map[v1.ResourceName]int64, error) {
	reservations := make(map[v1.ResourceName]int64)
	for k, v := range m {
		switch v1.ResourceName(k) {
		// Only memory resources are supported.
		case v1.ResourceMemory:
			q, err := parsePercentage(v)
			if err != nil {
				return nil, err
			}
			reservations[v1.ResourceName(k)] = q
		default:
			return nil, fmt.Errorf("cannot reserve %q resource", k)
		}
	}
	return &reservations, nil
}
