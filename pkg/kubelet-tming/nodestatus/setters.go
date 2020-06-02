package nodestatus

import (
	"k8s.io/api/core/v1"
	"net"
	"k8s.io/cloud-provider"
	"k8s.io/klog"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	goruntime "runtime"
	"time"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	"fmt"

	cadvisorapiv1 "github.com/google/cadvisor/info/v1"
	"math"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/kubernetes/pkg/kubelet-tming/cadvisor"
	//"k8s.io/kubernetes/pkg/apis/events"
	"k8s.io/kubernetes/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/version"
	//"k8s.io/kubernetes/pkg/kubelet-tming/cm"
	"strings"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/kubernetes/pkg/kubelet/events"
)

const (
	MaxNamesPerImageInNodeStatus = 5
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
		if utilfeature.DefaultFeatureGate.Enabled(features.LocalStorageCapacityIsolation) {
			requiredCapacities = append(requiredCapacities, v1.ResourceEphemeralStorage)
		}
		missingCapacities := []string{}
		for _, resource := range requiredCapacities {
			if _, found := node.Status.Capacity[resource]; !found {
				missingCapacities = append(missingCapacities, string(resource))
			}
		}
		if len(missingCapacities) > 0 {
			errs = append(errs, fmt.Errorf("Missing node capacity for resources: %s", strings.Join(missingCapacities, ", ")))
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
		needToRecordEvent := false
		for i := range node.Status.Conditions {
			if node.Status.Conditions[i].Type == v1.NodeReady {
				if node.Status.Conditions[i].Status == newNodeReadyCondition.Status {
					newNodeReadyCondition.LastTransitionTime = node.Status.Conditions[i].LastTransitionTime
				} else {
					newNodeReadyCondition.LastTransitionTime = currentTime
					needToRecordEvent = true
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
		if needToRecordEvent {
			if newNodeReadyCondition.Status == v1.ConditionTrue {
				recordEventFunc(v1.EventTypeNormal, events.NodeReady)
			} else {
				recordEventFunc(v1.EventTypeNormal, events.NodeNotReady)
				klog.Infof("Node became not ready: %+v", newNodeReadyCondition)
			}
		}

		return nil
	}
}

// PIDPressureCondition returns a Setter that updates the v1.NodePIDPressure condition on the node.
func PIDPressureCondition(nowFunc func() time.Time, // typically Kubelet.clock.Now
		pressureFunc func() bool, // typically Kubelet.evictionManager.IsUnderPIDPressure
		recordEventFunc func(eventType, event string), // typically Kubelet.recordNodeStatusEvent
) Setter {
	return func(node *v1.Node) error {
		currentTime := metav1.NewTime(nowFunc())
		var condition *v1.NodeCondition

		// Check if NodePIDPressure condition already exists and if it does, just pick it up for update.
		for i := range node.Status.Conditions {
			if node.Status.Conditions[i].Type == v1.NodePIDPressure {
				condition = &node.Status.Conditions[i]
			}
		}

		newCondition := false
		// If the NodePIDPressure condition doesn't exist, create one
		if condition == nil {
			condition = &v1.NodeCondition{
				Type:   v1.NodePIDPressure,
				Status: v1.ConditionUnknown,
			}
			// cannot be appended to node.Status.Conditions here because it gets
			// copied to the slice. So if we append to the slice here none of the
			// updates we make below are reflected in the slice.
			newCondition = true
		}

		// Update the heartbeat time
		condition.LastHeartbeatTime = currentTime

		// Note: The conditions below take care of the case when a new NodePIDPressure condition is
		// created and as well as the case when the condition already exists. When a new condition
		// is created its status is set to v1.ConditionUnknown which matches either
		// condition.Status != v1.ConditionTrue or
		// condition.Status != v1.ConditionFalse in the conditions below depending on whether
		// the kubelet is under PID pressure or not.
		if pressureFunc() {
			if condition.Status != v1.ConditionTrue {
				condition.Status = v1.ConditionTrue
				condition.Reason = "KubeletHasInsufficientPID"
				condition.Message = "kubelet has insufficient PID available"
				condition.LastTransitionTime = currentTime
				recordEventFunc(v1.EventTypeNormal, "NodeHasInsufficientPID")
			}
		} else if condition.Status != v1.ConditionFalse {
			condition.Status = v1.ConditionFalse
			condition.Reason = "KubeletHasSufficientPID"
			condition.Message = "kubelet has sufficient PID available"
			condition.LastTransitionTime = currentTime
			recordEventFunc(v1.EventTypeNormal, "NodeHasSufficientPID")
		}

		if newCondition {
			node.Status.Conditions = append(node.Status.Conditions, *condition)
		}
		return nil
	}
}


// DiskPressureCondition returns a Setter that updates the v1.NodeDiskPressure condition on the node.
func DiskPressureCondition(nowFunc func() time.Time, // typically Kubelet.clock.Now
		pressureFunc func() bool, // typically Kubelet.evictionManager.IsUnderDiskPressure
		recordEventFunc func(eventType, event string), // typically Kubelet.recordNodeStatusEvent
) Setter {
	return func(node *v1.Node) error {
		currentTime := metav1.NewTime(nowFunc())
		var condition *v1.NodeCondition

		// Check if NodeDiskPressure condition already exists and if it does, just pick it up for update.
		for i := range node.Status.Conditions {
			if node.Status.Conditions[i].Type == v1.NodeDiskPressure {
				condition = &node.Status.Conditions[i]
			}
		}

		newCondition := false
		// If the NodeDiskPressure condition doesn't exist, create one
		if condition == nil {
			condition = &v1.NodeCondition{
				Type:   v1.NodeDiskPressure,
				Status: v1.ConditionUnknown,
			}
			// cannot be appended to node.Status.Conditions here because it gets
			// copied to the slice. So if we append to the slice here none of the
			// updates we make below are reflected in the slice.
			newCondition = true
		}

		// Update the heartbeat time
		condition.LastHeartbeatTime = currentTime

		// Note: The conditions below take care of the case when a new NodeDiskPressure condition is
		// created and as well as the case when the condition already exists. When a new condition
		// is created its status is set to v1.ConditionUnknown which matches either
		// condition.Status != v1.ConditionTrue or
		// condition.Status != v1.ConditionFalse in the conditions below depending on whether
		// the kubelet is under disk pressure or not.
		if pressureFunc() {
			if condition.Status != v1.ConditionTrue {
				condition.Status = v1.ConditionTrue
				condition.Reason = "KubeletHasDiskPressure"
				condition.Message = "kubelet has disk pressure"
				condition.LastTransitionTime = currentTime
				recordEventFunc(v1.EventTypeNormal, "NodeHasDiskPressure")
			}
		} else if condition.Status != v1.ConditionFalse {
			condition.Status = v1.ConditionFalse
			condition.Reason = "KubeletHasNoDiskPressure"
			condition.Message = "kubelet has no disk pressure"
			condition.LastTransitionTime = currentTime
			recordEventFunc(v1.EventTypeNormal, "NodeHasNoDiskPressure")
		}

		if newCondition {
			node.Status.Conditions = append(node.Status.Conditions, *condition)
		}
		return nil
	}
}


func MemoryPressureCondition(nowFunc func() time.Time,
			pressureFunc func() bool,
			recordEventFunc func(eventType, event string),
) Setter {
	return func(node *v1.Node) error {
		currentTime := metav1.NewTime(nowFunc())
		var condition *v1.NodeCondition

		for i := range node.Status.Conditions {
			if node.Status.Conditions[i].Type == v1.NodeMemoryPressure {
				condition = &node.Status.Conditions[i]
			}
		}

		newCondition := false

		if condition == nil {
			condition = &v1.NodeCondition {
				Type: 		v1.NodeMemoryPressure,
				Status: 	v1.ConditionUnknown,
			}
			newCondition = true
		}

		condition.LastHeartbeatTime = currentTime


		if pressureFunc() {
			if condition.Status != v1.ConditionTrue {
				condition.Status = v1.ConditionTrue
				condition.Reason = "KubeletHasInsufficientMemory"
				condition.Message = "kubelet has insufficient memory available"
				condition.LastTransitionTime = currentTime
				recordEventFunc(v1.EventTypeNormal, "NodeHasInsufficientMemory")
			}
		} else if condition.Status != v1.ConditionFalse {
			condition.Status = v1.ConditionFalse
			condition.Reason = "KubeletHasSufficientMemory"
			condition.Message = "kubelet has sufficient memory available"
			condition.LastTransitionTime = currentTime
			recordEventFunc(v1.EventTypeNormal, "NodeHasSufficientMemory")
		}

		if newCondition {
			node.Status.Conditions = append(node.Status.Conditions, *condition)
		}

		return nil
	}
}


func VersionInfo(versionInfoFunc func() (*cadvisorapiv1.VersionInfo, error), // typically Kubelet.cadvisor.VersionInfo
			runtimeTypeFunc func() string, // typically Kubelet.containerRuntime.Type
			runtimeVersionFunc func() (kubecontainer.Version, error), // typically Kubelet.containerRuntime.Version
	) Setter {
	return func(node *v1.Node) error {
		verinfo, err := versionInfoFunc()
		if err != nil {
			// TODO(mtaufen): consider removing this log line, since returned error will be logged
			klog.Errorf("Error getting version info: %v", err)
			return fmt.Errorf("error getting version info: %v", err)
		}
		node.Status.NodeInfo.KernelVersion = verinfo.KernelVersion
		node.Status.NodeInfo.OSImage = verinfo.ContainerOsVersion

		runtimeVersion := "Unknown"
		if runtimeVer, err := runtimeVersionFunc(); err == nil {
			runtimeVersion = runtimeVer.String()
		}

		node.Status.NodeInfo.ContainerRuntimeVersion = fmt.Sprintf("%s://%s", runtimeTypeFunc(), runtimeVersion)

		node.Status.NodeInfo.KubeletVersion = version.Get().String()
		node.Status.NodeInfo.KubeProxyVersion = version.Get().String()

		return nil
	}
}



// DaemonEndpoints returns a Setter that updates the daemon endpoints on the node.
func DaemonEndpoints(daemonEndpoints *v1.NodeDaemonEndpoints) Setter {
	return func(node *v1.Node) error {
		node.Status.DaemonEndpoints = *daemonEndpoints
		return nil
	}
}


func MachineInfo(nodeName string,
		maxPods int,
		podsPerCore int,
		machineInfoFunc func() (*cadvisorapiv1.MachineInfo, error),
		capacityFunc func() v1.ResourceList,
		//devicePluginResourceCapacityFunc func() (v1.ResourceList, v1.ResourceList, []string),
		nodeAllocatableReservationFunc func() v1.ResourceList,
		recordEventFunc func(eventType, event, message string),
		) Setter {
	return func(node *v1.Node) error {
		if node.Status.Capacity == nil {
			node.Status.Capacity = v1.ResourceList{}
		}

		var devicePluginAllocatable v1.ResourceList
		//var devicePluginCapacity v1.ResourceList
		var removedDevicePlugins []string

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
				//recordEventFunc(v1.EventTypeWarning, events.NodeRebooted,
				//	fmt.Sprintf("Node %s has been rebooted, boot id: %s", nodeName, info.BootID))
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

func Images(nodeStatusMaxImages int32,
		imageListFunc func() ([]kubecontainer.Image, error)) Setter {
	return func(node *v1.Node) error {
		var imagesOnNode []v1.ContainerImage
		containerImages, err := imageListFunc()
		if err != nil {
			klog.Errorf("Error getting image list: %v", err)
			node.Status.Images = imagesOnNode
			return fmt.Errorf("error getting image list: %v", err)
		}

		if int(nodeStatusMaxImages) > -1 &&
			int(nodeStatusMaxImages) < len(containerImages) {
			containerImages = containerImages[0:nodeStatusMaxImages]
		}

		for _, image := range containerImages {
			names := append(image.RepoDigests, image.RepoTags...)
			if len(names) > MaxNamesPerImageInNodeStatus {
				names = names[0:MaxNamesPerImageInNodeStatus]
			}
			imagesOnNode = append(imagesOnNode, v1.ContainerImage{
				Names: 		names,
				SizeBytes: 	image.Size,
			})
		}
		node.Status.Images = imagesOnNode
		return nil
	}
}


func GoRuntime() Setter {
	return func(node *v1.Node) error {
		node.Status.NodeInfo.OperatingSystem = goruntime.GOOS
		node.Status.NodeInfo.Architecture = goruntime.GOARCH
		return nil
	}
}

func NodeAddress(nodeIP net.IP,
		validateNodeIPFunc func(net.IP) error,
		hostname string,
		hostnameOverridden bool,
		externalCloudProvider bool,
		cloud cloudprovider.Interface,
		nodeAddressesFunc func() ([]v1.NodeAddress, error),
) Setter {
	return func(node *v1.Node) error {
		if nodeIP != nil {
			klog.Infof("Using node IP: %q", nodeIP.String())
		}
		if cloud != nil {
			// TODO
		} else {
			var ipAddr net.IP
			var err error

			if nodeIP != nil {
				ipAddr = nodeIP
			} else if addr := net.ParseIP(hostname); addr != nil {
				ipAddr = addr
			} else {
				var addrs []net.IP
				addrs, _ = net.LookupIP(node.Name)
				for _, addr := range addrs {
					if err = validateNodeIPFunc(addr); err == nil {
						if addr.To4() != nil {
							ipAddr = addr
							break
						}
						if addr.To16() != nil && ipAddr == nil {
							ipAddr = addr
						}
					}
				}

				if ipAddr == nil {
					ipAddr, err = utilnet.ChooseHostInterface()
				}

				node.Status.Addresses = []v1.NodeAddress {
					{Type: v1.NodeInternalIP, Address: ipAddr.String()},
					{Type: v1.NodeHostName, Address: hostname},
				}
			}
		}
		return nil
	}
}
