package container

import (
	"time"
	"fmt"
	"k8s.io/klog"
)

type ContainerGCPolicy struct {
	MinAge 			time.Duration

	MaxPerPodContainer 	int

	MaxContainers 		int
}

type ContainerGC interface {

	GarbageCollect() 	error

	DeleteAllUnusedContainers() 	error

}

type SourcesReadyProvider interface {
	AllReady() 	bool
}

type realContainerGC struct {
	runtime 	Runtime

	policy 		ContainerGCPolicy

	sourcesReadyProvider 	SourcesReadyProvider
}

func NewContainerGC(runtime Runtime, policy ContainerGCPolicy, sourcesReadyProvider SourcesReadyProvider) (ContainerGC, error) {
	if policy.MinAge < 0 {
		return nil, fmt.Errorf("invalid minimum garbage collection age: %v", policy.MinAge)
	}

	return &realContainerGC{
		runtime: 			runtime,
		policy: 			policy,
		sourcesReadyProvider: 		sourcesReadyProvider,
	}, nil
}

func (cgc *realContainerGC) GarbageCollect() error {
	return cgc.runtime.GarbageCollect(cgc.policy, cgc.sourcesReadyProvider.AllReady(), false)
}

func (cgc *realContainerGC) DeleteAllUnusedContainers() error {
	klog.Infof("attempting to delete unused containers")
	return cgc.runtime.GarbageCollect(cgc.policy, cgc.sourcesReadyProvider.AllReady(), true)
}
