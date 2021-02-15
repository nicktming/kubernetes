package container

import "fmt"

// Manages garbage collection of dead containers.
//
// Implementation is thread-compatible.
type ContainerGC interface {
	// Garbage collect containers.
	GarbageCollect() error
}


type realContainerGC struct {
	// Container runtime
	runtime Runtime

	// Policy for garbage collection

	// sourcesReady
}


// New ContainerGC instance with the specified policy.
// TODO , policy ContainerGCPolicy, sourcesReadyProvider SourcesReadyProvider
func NewContainerGC(runtime Runtime) (ContainerGC, error) {
	//if policy.MinAge < 0 {
	//	return nil, fmt.Errorf("invalid minimum garbage collection age: %v", policy.MinAge)
	//}

	return &realContainerGC{
		runtime:              runtime,
		//policy:               policy,
		//sourcesReadyProvider: sourcesReadyProvider,
	}, nil
}

func (cgc *realContainerGC) GarbageCollect() error {
	return cgc.runtime.GarbageCollect(false)
}
