package pleg

import (
	"k8s.io/apimachinery/pkg/types"
)

type PodLifeCycleEventType string

const (
	ContainerStarted PodLifeCycleEventType = "ContainerStarted"
	ContainerDied PodLifeCycleEventType = "ContainerDied"
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

