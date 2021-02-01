package pleg

import (
	"k8s.io/apimachinery/pkg/types"
)

type PodLifeCycleEventType string

const (
	ContainerStarted PodLifeCycleEventType = "ContainerStarted"
	ContainerDied PodLifeCycleEventType = "ContainerDied"
	// ContainerChanged - event type when the new state of container is unknown.
	ContainerChanged PodLifeCycleEventType = "ContainerChanged"
)

type PodLifecycleEvent struct {
	ID types.UID

	Type PodLifeCycleEventType

	// TODO Data
	Data interface{}
}


type PodLifecycleEventGenerator interface {
	Start()
	Watch() chan *PodLifecycleEvent
}

