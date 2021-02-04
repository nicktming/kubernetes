package status

import (
	//"fmt"
	//"strings"

	"k8s.io/api/core/v1"
	//podutil "k8s.io/kubernetes/pkg/api/v1/pod"
)

const (
	UnknownContainerStatuses = "UnknownContainerStatuses"
	PodCompleted             = "PodCompleted"
	ContainersNotReady       = "ContainersNotReady"
	ContainersNotInitialized = "ContainersNotInitialized"
	ReadinessGatesNotReady   = "ReadinessGatesNotReady"
)


// GenerateContainersReadyCondition returns the status of "ContainersReady" condition.
// The status of "ContainersReady" condition is true when all containers are ready.
func GenerateContainersReadyCondition(spec *v1.PodSpec, containerStatuses []v1.ContainerStatus, podPhase v1.PodPhase) v1.PodCondition {
	if len(containerStatuses) == 0 {
		return v1.PodCondition {
			Type: v1.ContainersReady,
			Status: v1.ConditionUnknown,
		}
	}
	unready := 0
	success := 0
	for _, cs := range containerStatuses {
		if cs.State.Waiting != nil {
			unready++
		} else if cs.State.Terminated != nil {
			if cs.State.Terminated.ExitCode == 0 {
				success++
			} else {
				unready++
			}
		}
	}
	if unready > 0 {
		return v1.PodCondition{
			Type: v1.ContainersReady,
			Status: v1.ConditionFalse,
		}
	}
	return v1.PodCondition{
		Type: v1.ContainersReady,
		Status: v1.ConditionTrue,
	}
}


// GeneratePodReadyCondition returns "Ready" condition of a pod.
// The status of "Ready" condition is "True", if all containers in a pod are ready
// AND all matching conditions specified in the ReadinessGates have status equal to "True".

func GeneratePodReadyCondition(spec *v1.PodSpec, conditions []v1.PodCondition, containerStatuses []v1.ContainerStatus, podPhase v1.PodPhase) v1.PodCondition {
	containersCondition := GenerateContainersReadyCondition(spec, containerStatuses, podPhase)
	if containersCondition.Status != v1.ConditionTrue {
		return v1.PodCondition {
			Type: v1.PodReady,
			Status: containersCondition.Status,
			Reason: containersCondition.Reason,
			Message: containersCondition.Message,
		}
	}
	// TODO readiness
	return v1.PodCondition {
		Type: v1.PodReady,
		Status: v1.ConditionTrue,
	}
}


// GeneratePodInitializedCondition returns initialized condition if all init containers in a pod are ready, else it
// returns an uninitialized condition.
func GeneratePodInitializedCondition(spec *v1.PodSpec, containerStatuses []v1.ContainerStatus, podPhase v1.PodPhase) v1.PodCondition {
	return v1.PodCondition {
		Type: v1.PodInitialized,
		Status: v1.ConditionTrue,
	}
}