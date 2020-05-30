package cadvisor


import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path"
	"time"

	// Register supported container handlers.
	_ "github.com/google/cadvisor/container/containerd/install"
	_ "github.com/google/cadvisor/container/crio/install"
	_ "github.com/google/cadvisor/container/docker/install"
	_ "github.com/google/cadvisor/container/systemd/install"

	// Register cloud info providers.
	// TODO(#76660): Remove this once the cAdvisor endpoints are removed.
	_ "github.com/google/cadvisor/utils/cloudinfo/aws"
	_ "github.com/google/cadvisor/utils/cloudinfo/azure"
	_ "github.com/google/cadvisor/utils/cloudinfo/gce"

	"github.com/google/cadvisor/cache/memory"
	cadvisormetrics "github.com/google/cadvisor/container"
	"github.com/google/cadvisor/events"
	cadvisorapi "github.com/google/cadvisor/info/v1"
	cadvisorapiv2 "github.com/google/cadvisor/info/v2"
	"github.com/google/cadvisor/manager"
	"github.com/google/cadvisor/utils/sysfs"
	"k8s.io/klog"
)

type cadvisorClient struct {
	imageFsInfoProvider ImageFsInfoProvider
	rootPath            string
	manager.Manager
}

var _ Interface = new(cadvisorClient)

// TODO(vmarmol): Make configurable.
// The amount of time for which to keep stats in memory.
const statsCacheDuration = 2 * time.Minute
const maxHousekeepingInterval = 15 * time.Second
const defaultHousekeepingInterval = 10 * time.Second
const allowDynamicHousekeeping = true


func init() {
	// Override cAdvisor flag defaults.
	flagOverrides := map[string]string{
		// Override the default cAdvisor housekeeping interval.
		"housekeeping_interval": defaultHousekeepingInterval.String(),
		// Disable event storage by default.
		"event_storage_event_limit": "default=0",
		"event_storage_age_limit":   "default=0",
	}
	for name, defaultValue := range flagOverrides {
		if f := flag.Lookup(name); f != nil {
			f.DefValue = defaultValue
			f.Value.Set(defaultValue)
		} else {
			klog.Errorf("Expected cAdvisor flag %q not found", name)
		}
	}
}

func New(ImageFsInfoProvider ImageFsInfoProvider, rootPath string, cgroupRoots []string, usingLegacyStats bool) (Interface, error) {
	sysFs := sysfs.NewRealSysFs()

	includedMetrics := cadvisormetrics.MetricSet {
		cadvisormetrics.CpuUsageMetrics:         struct{}{},
		cadvisormetrics.MemoryUsageMetrics:      struct{}{},
		cadvisormetrics.CpuLoadMetrics:          struct{}{},
		cadvisormetrics.DiskIOMetrics:           struct{}{},
		cadvisormetrics.NetworkUsageMetrics:     struct{}{},
		cadvisormetrics.AcceleratorUsageMetrics: struct{}{},
		cadvisormetrics.AppMetrics:              struct{}{},
	}

	if usingLegacyStats {
		includedMetrics[cadvisormetrics.DiskUsageMetrics] = struct {}{}
	}

	m, err := manager.New(memory.New(statsCacheDuration, nil), sysFs, maxHousekeepingInterval, allowDynamicHousekeeping, includedMetrics, http.DefaultClient, cgroupRoots)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(rootPath); err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(path.Clean(rootPath), 0750); err != nil {
				return nil, fmt.Errorf("error creating root directory %q: %v", rootPath, err)
			} else {
				return nil, fmt.Errorf("failed to Stat %q: %v", rootPath, err)
			}
		}
	}

	cadvisorClient := &cadvisorClient{
		imageFsInfoProvider: 		ImageFsInfoProvider,
		rootPath:			rootPath,
		Manager: 			m,
	}

	return cadvisorClient, nil
}






















































