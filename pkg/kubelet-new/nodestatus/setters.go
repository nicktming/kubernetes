package nodestatus

import (
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cadvisorapiv1 "github.com/google/cadvisor/info/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"time"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/cadvisor"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/events"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"math"
	"fmt"
)

type Setter func(node *v1.Node) error

// ReadyCondition returns a Setter that updates the v1.NodeReady condition on the node.
func ReadyCondition(
nowFunc func() time.Time, // typically Kubelet.clock.Now
//runtimeErrorsFunc func() error, // typically Kubelet.runtimeState.runtimeErrors
//networkErrorsFunc func() error, // typically Kubelet.runtimeState.networkErrors
//storageErrorsFunc func() error, // typically Kubelet.runtimeState.storageErrors
//appArmorValidateHostFunc func() error, // typically Kubelet.appArmorValidator.ValidateHost, might be nil depending on whether there was an appArmorValidator
//cmStatusFunc func() cm.Status, // typically Kubelet.containerManager.Status
recordEventFunc func(eventType, event string), // typically Kubelet.recordNodeStatusEvent
) Setter {
	return func(node *v1.Node) error {

		currentTime := metav1.NewTime(nowFunc())
		newNodeReadyCondition := v1.NodeCondition{
			Type:			v1.NodeReady,
			Status:			v1.ConditionTrue,
			Reason:			"KubeletReady",
			Message:		"kubelet is posting ready status",
			LastHeartbeatTime: 	currentTime,
		}

		//errs := []error{runtimeErrorsFunc(), networkErrorsFunc(), storageErrorsFunc()}

		errs := []error{}
		requiredCapacities := []v1.ResourceName{v1.ResourceCPU, v1.ResourceMemory, v1.ResourcePods}
		//if utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
		//	requiredCapacities = append(requiredCapacities, v1.ResourceEphemeralStorage)
		//}
		missingCapacities := []string{}
		for _, resource := range requiredCapacities {
			if _, found := node.Status.Capacity[resource]; !found {
				missingCapacities = append(missingCapacities, string(resource))
			}
		}
		if len(missingCapacities) > 0 {
			//errs = append(errs, fmt.Errorf("Missing node capacity for resources: %s", strings.Join(missingCapacities, ", ")))
		}
		if aggregatedErr := errors.NewAggregate(errs); aggregatedErr != nil {
			newNodeReadyCondition = v1.NodeCondition{
				Type:              v1.NodeReady,
				Status:            v1.ConditionFalse,
				Reason:            "KubeletNotReady",
				Message:           aggregatedErr.Error(),
				LastHeartbeatTime: currentTime,
			}
		}

		// TODO

		//if appArmorValidateHostFunc != nil && newNodeReadyCondition.Status == v1.ConditionTrue {
		//	if err := appArmorValidateHostFunc(); err == nil {
		//		newNodeReadyCondition.Message = fmt.Sprintf("%s. AppArmor enabled", newNodeReadyCondition.Message)
		//	}
		//}
		//
		//// Record any soft requirements that were not met in the container manager.
		//status := cmStatusFunc()
		//if status.SoftRequirements != nil {
		//	newNodeReadyCondition.Message = fmt.Sprintf("%s. WARNING: %s", newNodeReadyCondition.Message, status.SoftRequirements.Error())
		//}

		readyConditionUpdated := false
		//needToRecordEvent := false
		for i := range node.Status.Conditions {
			if node.Status.Conditions[i].Type == v1.NodeReady {
				if node.Status.Conditions[i].Status == newNodeReadyCondition.Status {
					newNodeReadyCondition.LastTransitionTime = node.Status.Conditions[i].LastTransitionTime
				} else {
					newNodeReadyCondition.LastTransitionTime = currentTime
					//needToRecordEvent = true
				}
				node.Status.Conditions[i] = newNodeReadyCondition
				readyConditionUpdated = true
				break
			}
		}
		if !readyConditionUpdated {
			newNodeReadyCondition.LastTransitionTime = currentTime
			node.Status.Conditions = append(node.Status.Conditions, newNodeReadyCondition)
		}
		//if needToRecordEvent {
		//	if newNodeReadyCondition.Status == v1.ConditionTrue {
		//		recordEventFunc(v1.EventTypeNormal, events.NodeReady)
		//	} else {
		//		recordEventFunc(v1.EventTypeNormal, events.NodeNotReady)
		//		klog.Infof("Node became not ready: %+v", newNodeReadyCondition)
		//	}
		//}

		return nil
	}
}


// MachineInfo returns a Setter that updates machine-related information on the node.
func MachineInfo(nodeName string,
	maxPods int,
	podsPerCore int,
	machineInfoFunc func() (*cadvisorapiv1.MachineInfo, error), // typically Kubelet.GetCachedMachineInfo
	capacityFunc func() v1.ResourceList, // typically Kubelet.containerManager.GetCapacity
	devicePluginResourceCapacityFunc func() (v1.ResourceList, v1.ResourceList, []string), // typically Kubelet.containerManager.GetDevicePluginResourceCapacity
	nodeAllocatableReservationFunc func() v1.ResourceList, // typically Kubelet.containerManager.GetNodeAllocatableReservation
	recordEventFunc func(eventType, event, message string), // typically Kubelet.recordEvent
	) Setter {
	return func(node *v1.Node) error {
		// Note: avoid blindly overwriting the capacity in case opaque
		//       resources are being advertised.
		if node.Status.Capacity == nil {
			node.Status.Capacity = v1.ResourceList{}
		}

		var devicePluginAllocatable v1.ResourceList
		//var devicePluginCapacity v1.ResourceList
		var removedDevicePlugins []string

		// TODO: Post NotReady if we cannot get MachineInfo from cAdvisor. This needs to start
		// cAdvisor locally, e.g. for test-cmd.sh, and in integration test.
		info, err := machineInfoFunc()
		if err != nil {
			// TODO(roberthbailey): This is required for test-cmd.sh to pass.
			// See if the test should be updated instead.
			node.Status.Capacity[v1.ResourceCPU] = *resource.NewMilliQuantity(0, resource.DecimalSI)
			node.Status.Capacity[v1.ResourceMemory] = resource.MustParse("0Gi")
			node.Status.Capacity[v1.ResourcePods] = *resource.NewQuantity(int64(maxPods), resource.DecimalSI)
			klog.Errorf("Error getting machine info: %v", err)
		} else {
			node.Status.NodeInfo.MachineID = info.MachineID
			node.Status.NodeInfo.SystemUUID = info.SystemUUID

			for rName, rCap := range cadvisor.CapacityFromMachineInfo(info) {
				node.Status.Capacity[rName] = rCap
			}

			if podsPerCore > 0 {
				node.Status.Capacity[v1.ResourcePods] = *resource.NewQuantity(
					int64(math.Min(float64(info.NumCores*podsPerCore), float64(maxPods))), resource.DecimalSI)
			} else {
				node.Status.Capacity[v1.ResourcePods] = *resource.NewQuantity(
					int64(maxPods), resource.DecimalSI)
			}

			if node.Status.NodeInfo.BootID != "" &&
				node.Status.NodeInfo.BootID != info.BootID {
				// TODO: This requires a transaction, either both node status is updated
				// and event is recorded or neither should happen, see issue #6055.
				recordEventFunc(v1.EventTypeWarning, events.NodeRebooted,
					fmt.Sprintf("Node %s has been rebooted, boot id: %s", nodeName, info.BootID))
			}
			node.Status.NodeInfo.BootID = info.BootID

			if utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
				// TODO: all the node resources should use ContainerManager.GetCapacity instead of deriving the
				// capacity for every node status request
				initialCapacity := capacityFunc()
				if initialCapacity != nil {
					if v, exists := initialCapacity[v1.ResourceEphemeralStorage]; exists {
						node.Status.Capacity[v1.ResourceEphemeralStorage] = v
					}
				}
			}

			//devicePluginCapacity, devicePluginAllocatable, removedDevicePlugins = devicePluginResourceCapacityFunc()
			//if devicePluginCapacity != nil {
			//	for k, v := range devicePluginCapacity {
			//		if old, ok := node.Status.Capacity[k]; !ok || old.Value() != v.Value() {
			//			klog.V(2).Infof("Update capacity for %s to %d", k, v.Value())
			//		}
			//		node.Status.Capacity[k] = v
			//	}
			//}

			for _, removedResource := range removedDevicePlugins {
				klog.V(2).Infof("Set capacity for %s to 0 on device removal", removedResource)
				// Set the capacity of the removed resource to 0 instead of
				// removing the resource from the node status. This is to indicate
				// that the resource is managed by device plugin and had been
				// registered before.
				//
				// This is required to differentiate the device plugin managed
				// resources and the cluster-level resources, which are absent in
				// node status.
				node.Status.Capacity[v1.ResourceName(removedResource)] = *resource.NewQuantity(int64(0), resource.DecimalSI)
			}
		}

		// Set Allocatable.
		if node.Status.Allocatable == nil {
			node.Status.Allocatable = make(v1.ResourceList)
		}
		// Remove extended resources from allocatable that are no longer
		// present in capacity.
		for k := range node.Status.Allocatable {
			_, found := node.Status.Capacity[k]
			if !found && v1helper.IsExtendedResourceName(k) {
				delete(node.Status.Allocatable, k)
			}
		}
		allocatableReservation := nodeAllocatableReservationFunc()
		for k, v := range node.Status.Capacity {
			value := *(v.Copy())
			if res, exists := allocatableReservation[k]; exists {
				value.Sub(res)
			}
			if value.Sign() < 0 {
				// Negative Allocatable resources don't make sense.
				value.Set(0)
			}
			node.Status.Allocatable[k] = value
		}

		if devicePluginAllocatable != nil {
			for k, v := range devicePluginAllocatable {
				if old, ok := node.Status.Allocatable[k]; !ok || old.Value() != v.Value() {
					klog.V(2).Infof("Update allocatable for %s to %d", k, v.Value())
				}
				node.Status.Allocatable[k] = v
			}
		}
		// for every huge page reservation, we need to remove it from allocatable memory
		for k, v := range node.Status.Capacity {
			if v1helper.IsHugePageResourceName(k) {
				allocatableMemory := node.Status.Allocatable[v1.ResourceMemory]
				value := *(v.Copy())
				allocatableMemory.Sub(value)
				if allocatableMemory.Sign() < 0 {
					// Negative Allocatable resources don't make sense.
					allocatableMemory.Set(0)
				}
				node.Status.Allocatable[v1.ResourceMemory] = allocatableMemory
			}
		}
		return nil
	}
}
