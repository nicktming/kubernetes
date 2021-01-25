package pleg

import (
	"k8s.io/apimachinery/pkg/types"
)

type PodLifeCycleEventType string

const (
	ContainerStarted PodLifeCycleEventType = "ContainerStarted"
)

type PodLifecycleEvent struct {
	ID types.UID

	Type PodLifeCycleEventType

	// TODO Data
}


type PodLifecycleEventGenerator interface {
	Start()
}

