package types

import (
	"k8s.io/api/core/v1"
)

// PodConditionsByKubelet is the list of pod conditions owned by kubelet
var PodConditionsByKubelet = []v1.PodConditionType{
	v1.PodScheduled,
	v1.PodReady,
	v1.PodInitialized,
	v1.PodReasonUnschedulable,
	v1.ContainersReady,
}

// PodConditionByKubelet returns if the pod condition type is owned by kubelet
func PodConditionByKubelet(conditionType v1.PodConditionType) bool {
	for _, c := range PodConditionsByKubelet {
		if c == conditionType {
			return true
		}
	}
	return false
}
