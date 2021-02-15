package prober

import (
	"k8s.io/client-go/tools/record"
	execprobe "k8s.io/kubernetes/pkg/probe/exec"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	"k8s.io/kubernetes/pkg/kubelet-new/prober/results"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	//"k8s.io/kubernetes/pkg/kubelet/events"
	"k8s.io/klog"
	"k8s.io/api/core/v1"
	"fmt"
	"k8s.io/kubernetes/pkg/probe"
	"k8s.io/utils/exec"
	"time"
	"io"
)

const maxProbeRetries = 3

// Prober helps to check the liveness/readiness of a container.
type prober struct {
	exec execprobe.Prober

	// TODO refManager runner
	recorder   record.EventRecorder
	runner kubecontainer.ContainerCommandRunner
}

// NewProber creates a Prober, it takes a command runner and
// several container info managers.
func newProber(
	runner kubecontainer.ContainerCommandRunner,
	recorder record.EventRecorder) *prober {

	const followNonLocalRedirects = false
	return &prober{
		exec:          execprobe.New(),
		//readinessHttp: httprobe.New(followNonLocalRedirects),
		//livenessHttp:  httprobe.New(followNonLocalRedirects),
		//tcp:           tcprobe.New(),
		runner:        runner,
		//refManager:    refManager,
		recorder:      recorder,
	}
}

// probe probes the container.
func (pb *prober) probe(probeType probeType, pod *v1.Pod, status v1.PodStatus, container v1.Container, containerID kubecontainer.ContainerID) (results.Result, error) {
	var probeSpec *v1.Probe
	switch probeType {
	case readiness:
		probeSpec = container.ReadinessProbe
	case liveness:
		probeSpec = container.LivenessProbe
	default:
		return results.Failure, fmt.Errorf("Unknown probe type: %q", probeType)
	}

	ctrName := fmt.Sprintf("%s:%s", format.Pod(pod), container.Name)
	if probeSpec == nil {
		klog.Warningf("%s probe for %s is nil", probeType, ctrName)
		return results.Success, nil
	}

	result, output, err := pb.runProbeWithRetries(probeType, probeSpec, pod, status, container, containerID, maxProbeRetries)
	if err != nil || (result != probe.Success && result != probe.Warning) {
		// Probe failed in one way or another.
		// TODO ref manager
		//ref, hasRef := pb.refManager.GetRef(containerID)
		//if !hasRef {
		//	klog.Warningf("No ref for container %q (%s)", containerID.String(), ctrName)
		//}
		if err != nil {
			klog.V(1).Infof("%s probe for %q errored: %v", probeType, ctrName, err)
			//if hasRef {
			//	pb.recorder.Eventf(ref, v1.EventTypeWarning, events.ContainerUnhealthy, "%s probe errored: %v", probeType, err)
			//}
		} else { // result != probe.Success
			klog.V(1).Infof("%s probe for %q failed (%v): %s", probeType, ctrName, result, output)
			//if hasRef {
			//	pb.recorder.Eventf(ref, v1.EventTypeWarning, events.ContainerUnhealthy, "%s probe failed: %s", probeType, output)
			//}
		}
		return results.Failure, err
	}
	if result == probe.Warning {
		//if ref, hasRef := pb.refManager.GetRef(containerID); hasRef {
		//	pb.recorder.Eventf(ref, v1.EventTypeWarning, events.ContainerProbeWarning, "%s probe warning: %s", probeType, output)
		//}
		klog.V(3).Infof("%s probe for %q succeeded with a warning: %s", probeType, ctrName, output)
	} else {
		klog.V(3).Infof("%s probe for %q succeeded", probeType, ctrName)
	}
	return results.Success, nil
}

// runProbeWithRetries tries to probe the container in a finite loop, it returns the last result
// if it never succeeds.
func (pb *prober) runProbeWithRetries(probeType probeType, p *v1.Probe, pod *v1.Pod, status v1.PodStatus, container v1.Container, containerID kubecontainer.ContainerID, retries int) (probe.Result, string, error) {
	var err error
	var result probe.Result
	var output string
	for i := 0; i < retries; i++ {
		result, output, err = pb.runProbe(probeType, p, pod, status, container, containerID)
		if err == nil {
			return result, output, nil
		}
	}
	return result, output, err
}

func (pb *prober) runProbe(probeType probeType, p *v1.Probe, pod *v1.Pod, status v1.PodStatus, container v1.Container, containerID kubecontainer.ContainerID) (probe.Result, string, error) {
	timeout := time.Duration(p.TimeoutSeconds) * time.Second
	if p.Exec != nil {
		//klog.Infof("+++++++++++Exec-Probe Pod: %v, Container: %v, Command: %v", pod, container, p.Exec.Command)
		command := kubecontainer.ExpandContainerCommandOnlyStatic(p.Exec.Command, container.Env)
		//klog.Infof("+++++++Exec-Probe Pod: %v, Container: %v, after expand Command: %v", pod, container, command)
		return pb.exec.Probe(pb.newExecInContainer(container, containerID, command, timeout))
	}
	//if p.HTTPGet != nil {
	//	scheme := strings.ToLower(string(p.HTTPGet.Scheme))
	//	host := p.HTTPGet.Host
	//	if host == "" {
	//		host = status.PodIP
	//	}
	//	port, err := extractPort(p.HTTPGet.Port, container)
	//	if err != nil {
	//		return probe.Unknown, "", err
	//	}
	//	path := p.HTTPGet.Path
	//	klog.V(4).Infof("HTTP-Probe Host: %v://%v, Port: %v, Path: %v", scheme, host, port, path)
	//	url := formatURL(scheme, host, port, path)
	//	headers := buildHeader(p.HTTPGet.HTTPHeaders)
	//	klog.V(4).Infof("HTTP-Probe Headers: %v", headers)
	//	if probeType == liveness {
	//		return pb.livenessHttp.Probe(url, headers, timeout)
	//	} else { // readiness
	//		return pb.readinessHttp.Probe(url, headers, timeout)
	//	}
	//}
	//if p.TCPSocket != nil {
	//	port, err := extractPort(p.TCPSocket.Port, container)
	//	if err != nil {
	//		return probe.Unknown, "", err
	//	}
	//	host := p.TCPSocket.Host
	//	if host == "" {
	//		host = status.PodIP
	//	}
	//	klog.V(4).Infof("TCP-Probe Host: %v, Port: %v, Timeout: %v", host, port, timeout)
	//	return pb.tcp.Probe(host, port, timeout)
	//}
	klog.Warningf("Failed to find probe builder for container: %v", container)
	return probe.Unknown, "", fmt.Errorf("Missing probe handler for %s:%s", format.Pod(pod), container.Name)
}

type execInContainer struct {
	// run executes a command in a container. Combined stdout and stderr output is always returned. An
	// error is returned if one occurred.
	run func() ([]byte, error)
}

func (pb *prober) newExecInContainer(container v1.Container, containerID kubecontainer.ContainerID, cmd []string, timeout time.Duration) exec.Cmd {
	return execInContainer{func() ([]byte, error) {
		return pb.runner.RunInContainer(containerID, cmd, timeout)
	}}
}

func (eic execInContainer) Run() error {
	return fmt.Errorf("unimplemented")
}

func (eic execInContainer) CombinedOutput() ([]byte, error) {
	return eic.run()
}

func (eic execInContainer) Output() ([]byte, error) {
	return nil, fmt.Errorf("unimplemented")
}

func (eic execInContainer) SetDir(dir string) {
	//unimplemented
}

func (eic execInContainer) SetStdin(in io.Reader) {
	//unimplemented
}

func (eic execInContainer) SetStdout(out io.Writer) {
	//unimplemented
}

func (eic execInContainer) SetStderr(out io.Writer) {
	//unimplemented
}

func (eic execInContainer) SetEnv(env []string) {
	//unimplemented
}

func (eic execInContainer) Stop() {
	//unimplemented
}

func (eic execInContainer) Start() error {
	return fmt.Errorf("unimplemented")
}

func (eic execInContainer) Wait() error {
	return fmt.Errorf("unimplemented")
}

func (eic execInContainer) StdoutPipe() (io.ReadCloser, error) {
	return nil, fmt.Errorf("unimplemented")
}

func (eic execInContainer) StderrPipe() (io.ReadCloser, error) {
	return nil, fmt.Errorf("unimplemented")
}
