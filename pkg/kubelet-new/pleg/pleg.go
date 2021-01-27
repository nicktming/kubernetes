package pleg

import (
	"k8s.io/apimachinery/pkg/types"
)

type PodLifeCycleEventType string

const (
	ContainerStarted PodLifeCycleEventType = "ContainerStarted"
	ContainerStarted PodLifeCycleEventType = "ContainerDied"
)

type PodLifecycleEvent struct {
	ID types.UID

	Type PodLifeCycleEventType

	// TODO Data
	Data interface{}
}


type PodLifecycleEventGenerator interface {
	Start()
}

