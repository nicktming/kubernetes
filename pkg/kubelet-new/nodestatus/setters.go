package nodestatus

import (
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
	"fmt"
	"strings"
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
