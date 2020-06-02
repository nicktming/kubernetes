package pleg

import "k8s.io/apimachinery/pkg/types"

type PodLifeCycleEventType string


const (
	// ContainerStarted - event type when the new state of container is running.
	ContainerStarted PodLifeCycleEventType = "ContainerStarted"
	// ContainerDied - event type when the new state of container is exited.
	ContainerDied PodLifeCycleEventType = "ContainerDied"
	// ContainerRemoved - event type when the old state of container is exited.
	ContainerRemoved PodLifeCycleEventType = "ContainerRemoved"
	// PodSync is used to trigger syncing of a pod when the observed change of
	// the state of the pod cannot be captured by any single event above.
	PodSync PodLifeCycleEventType = "PodSync"
	// ContainerChanged - event type when the new state of container is unknown.
	ContainerChanged PodLifeCycleEventType = "ContainerChanged"
)

type PodLifecycleEvent struct {
	ID 		types.UID

	Type 		PodLifeCycleEventType

	Data 		interface{}
}

type PodLifecycleEventGenerator interface {
	Start()

	Watch() chan *PodLifecycleEvent

	Healthy() (bool, error)
}

