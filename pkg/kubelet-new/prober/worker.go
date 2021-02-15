package prober

import (
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet-new/prober/results"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	"time"
	"math/rand"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	"k8s.io/klog"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
)


// worker handles the periodic probing of its assigned container. Each worker has a go-routine
// associated with it which runs the probe loop until the container permanently terminates, or the
// stop channel is closed. The worker uses the probe Manager's statusManager to get up-to-date
// container IDs.
type worker struct {
	// Channel for stopping the probe.
	stopCh chan struct{}

	// The pod containing this probe (read-only)
	pod *v1.Pod

	// The container to probe (read-only)
	container v1.Container

	// Describes the probe configuration (read-only)
	spec *v1.Probe

	// The type of the worker.
	probeType probeType

	// The probe value during the initial delay.
	initialValue results.Result

	// Where to store this workers results.
	resultsManager results.Manager
	probeManager   *manager

	// The last known container ID for this worker.
	containerID kubecontainer.ContainerID
	// The last probe result for this worker.
	lastResult results.Result
	// How many times in a row the probe has returned the same result.
	resultRun int

	// If set, skip probing.
	onHold bool

}

// Creates and starts a new probe worker.
func newWorker(
	m *manager,
	probeType probeType,
	pod *v1.Pod,
	container v1.Container) *worker {

	w := &worker{
		stopCh:       make(chan struct{}, 1), // Buffer so stop() can be non-blocking.
		pod:          pod,
		container:    container,
		probeType:    probeType,
		probeManager: m,
	}
	switch probeType {
	case readiness:
		w.spec = container.ReadinessProbe
		w.resultsManager = m.readinessManager
		w.initialValue = results.Failure
	//case liveness:
	//	w.spec = container.LivenessProbe
	//	w.resultsManager = m.livenessManager
	//	w.initialValue = results.Success
	}
	return w
}

// run periodically probes the container.
func (w *worker) run() {
	probeTickerPeriod := time.Duration(w.spec.PeriodSeconds) * time.Second

	// If kubelet restarted the probes could be started in rapid succession.
	// Let the worker wait for a random portion of tickerPeriod before probing.
	time.Sleep(time.Duration(rand.Float64() * float64(probeTickerPeriod)))

	probeTicker := time.NewTicker(probeTickerPeriod)

	defer func() {
		// Clean up.
		probeTicker.Stop()
		if !w.containerID.IsEmpty() {
			w.resultsManager.Remove(w.containerID)
		}

		w.probeManager.removeWorker(w.pod.UID, w.container.Name, w.probeType)
	}()

	probeLoop:
	for w.doProbe() {
		// Wait for next probe tick.
		select {
		case <-w.stopCh:
			break probeLoop
		case <-probeTicker.C:
		// continue
		}
	}
}

// stop stops the probe worker. The worker handles cleanup and removes itself from its manager.
// It is safe to call stop multiple times.
func (w *worker) stop() {
	select {
	case w.stopCh <- struct{}{}:
	default: // Non-blocking.
	}
}

// doProbe probes the container once and records the result.
// Returns whether the worker should continue.
func (w *worker) doProbe() (keepGoing bool) {
	defer func() {
		recover()
	}() // Actually eat panics (HandleCrash takes care of logging)
	defer runtime.HandleCrash(func(_ interface{}) {
		keepGoing = true
	})

	status, ok := w.probeManager.statusManager.GetPodStatus(w.pod.UID)
	if !ok {
		// Either the pod has not been created yet, or it was already deleted.
		klog.Infof("No status for pod: %v", format.Pod(w.pod))
		return true
	}


	// TODO // Worker should terminate if pod is terminated.

	c, ok := podutil.GetContainerStatus(status.ContainerStatuses, w.container.Name)
	if !ok || len(c.ContainerID) == 0 {
		// Either the container has not been created yet, or it was deleted.
		klog.Infof("Probe target container not found: %v - %v",
			format.Pod(w.pod), w.container.Name)
		return true // Wait for more information.
	}

	if w.containerID.String() != c.ContainerID {
		if !w.containerID.IsEmpty() {
			w.resultsManager.Remove(w.containerID)
		}
		w.containerID = kubecontainer.ParseContainerID(c.ContainerID)
		w.resultsManager.Set(w.containerID, w.initialValue, w.pod)
		klog.Infof("Probe target container found: %v - %v(%v)",
			format.Pod(w.pod), w.container.Name, w.containerID.String())
		// We've got a new container; resume probing.
		w.onHold = false
	}

	if w.onHold {
		// Worker is on hold until there is a new container.
		return true
	}

	if c.State.Running == nil {
		klog.Infof("Non-running container probed: %v - %v",
			format.Pod(w.pod), w.container.Name)
		if !w.containerID.IsEmpty() {
			w.resultsManager.Set(w.containerID, results.Failure, w.pod)
		}
		// TODO should be more specific
		return true
	}

	// TODO: in order for exec probes to correctly handle downward API env, we must be able to reconstruct
	// the full container environment here, OR we must make a call to the CRI in order to get those environment
	// values from the running container.
	result, err := w.probeManager.prober.probe(w.probeType, w.pod, status, w.container, w.containerID)

	klog.Infof("=====>probe containerid: %v, result %v, err: %v",
		w.containerID, result, err)

	if err != nil {
		// Prober error, throw away the result.
		return true
	}

	// TODO failure times

	w.resultsManager.Set(w.containerID, result, w.pod)


	// TODO liveness

	return true
}