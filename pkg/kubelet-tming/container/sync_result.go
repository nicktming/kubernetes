package container

import (
	"errors"
)


// Container Terminated and Kubelet is backing off the restart
var ErrCrashLoopBackOff = errors.New("CrashLoopBackOff")

var (
	// ErrContainerNotFound returned when a container in the given pod with the
	// given container name was not found, amongst those managed by the kubelet.
	ErrContainerNotFound = errors.New("no matching container")
)

