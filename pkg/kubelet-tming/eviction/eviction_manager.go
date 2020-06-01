package eviction

import (
	"k8s.io/apimachinery/pkg/util/clock"
	"time"
	"k8s.io/klog"
	"k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
	"sync"
	"k8s.io/kubernetes/pkg/kubelet-tming/server/stats"
)

type managerImpl struct {

	clock 		clock.Clock
	config 		Config

	// protects access to internal state
	sync.RWMutex

	// node conditions are the set of conditions present
	nodeConditions []v1.NodeConditionType

	// used to measure usage stats on system
	summaryProvider stats.SummaryProvider

}

func NewManager(summaryProvider stats.SummaryProvider) Manager {

	manager := &managerImpl {
		summaryProvider: 	summaryProvider,
	}

	manager.nodeConditions = make([]v1.NodeConditionType, 0)

	return manager
}

// IsUnderMemoryPressure returns true if the node is under memory pressure.
func (m *managerImpl) IsUnderMemoryPressure() bool {
	m.RLock()
	defer m.RUnlock()
	return hasNodeCondition(m.nodeConditions, v1.NodeMemoryPressure)
}

// IsUnderDiskPressure returns true if the node is under disk pressure.
func (m *managerImpl) IsUnderDiskPressure() bool {
	m.RLock()
	defer m.RUnlock()
	return hasNodeCondition(m.nodeConditions, v1.NodeDiskPressure)
}

// IsUnderPIDPressure returns true if the node is under PID pressure.
func (m *managerImpl) IsUnderPIDPressure() bool {
	m.RLock()
	defer m.RUnlock()
	return hasNodeCondition(m.nodeConditions, v1.NodePIDPressure)
}



func (m *managerImpl) Start(diskInfoProvider DiskInfoProvider, podFunc ActivePodsFunc, podCleanedUpFunc PodCleanedUpFunc, monitoringInterval time.Duration) {
	//thresholdHandler := func(message string) {
	//	klog.Infof(message)
	//	m.synchronize(diskInfoProvider, podFunc)
	//}

	// TODO

	//if m.config.KernelMemcgNotification {
	//	for _, threshold := range m.config.Thresholds {
	//		if threshold.Signal == evictionapi.SignalMemoryAvailable || threshold.Signal == evictionapi.SignalAllocatableMemoryAvailable {
	//			notifier, err := NewMemoryThresholdNotifier(threshold, m.config.PodCgroupRoot, &CgroupNotifierFactory{}, thresholdHandler)
	//			if err != nil {
	//				klog.Warningf("eviction manager: failed to create memory threshold notifier: %v", err)
	//			} else {
	//				go notifier.Start()
	//				m.thresholdNotifiers = append(m.thresholdNotifiers, notifier)
	//			}
	//		}
	//	}
	//}
	//// start the eviction manager monitoring
	//go func() {
	//	for {
	//		if evictedPods := m.synchronize(diskInfoProvider, podFunc); evictedPods != nil {
	//			klog.Infof("eviction manager: pods %s evicted, waiting for pod to be cleaned up", format.Pods(evictedPods))
	//			m.waitForPodsCleanup(podCleanedUpFunc, evictedPods)
	//		} else {
	//			time.Sleep(monitoringInterval)
	//		}
	//	}
	//}()

	klog.Infof("eviction manager start!")
}

func (m *managerImpl) synchronize(DiskInfoProvider DiskInfoProvider, podFunc ActivePodsFunc) []*v1.Pod {
	thresholds := m.config.Thresholds
	if len(thresholds) == 0 && !utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
		return nil
	}
	klog.V(3).Infof("eviction manager: synchronize housekeeping")

	res := make([]*v1.Pod, 0)

	return res
}