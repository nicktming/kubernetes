package kuberuntime

import (
	"k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	kubetypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet-new/types"
	"fmt"
	"net"
	"sort"
	"gopkg.in/square/go-jose.v2/json"
)


// getKubeletSandboxes lists all (or just the running) sandboxes managed by kubelet.
func (m *kubeGenericRuntimeManager) getKubeletSandboxes(all bool) ([]*runtimeapi.PodSandbox, error) {
	var filter *runtimeapi.PodSandboxFilter
	if !all {
		readyState := runtimeapi.PodSandboxState_SANDBOX_READY
		filter = &runtimeapi.PodSandboxFilter{
			State: &runtimeapi.PodSandboxStateValue{
				State: readyState,
			},
		}
	}

	resp, err := m.runtimeService.ListPodSandbox(filter)
	if err != nil {
		klog.Errorf("ListPodSandbox failed: %v", err)
		return nil, err
	}

	return resp, nil
}

func (m *kubeGenericRuntimeManager) createPodSandbox(pod *v1.Pod, attempt uint32) (string, string, error) {
	podSandboxConfig, err := m.generatePodSandboxConfig(pod, attempt)
	if err != nil {
		message := fmt.Sprintf("GeneratePodSandboxConfig for pod %q failed: %v", format.Pod(pod), err)
		klog.Error(message)
		return "", message, err
	}
	// Create pod logs directory
	err = m.osInterface.MkdirAll(podSandboxConfig.LogDirectory, 0755)
	if err != nil {
		message := fmt.Sprintf("Create pod log directory for pod %q failed: %v", format.Pod(pod), err)
		klog.Errorf(message)
		return "", message, err
	}
	runtimeHandler := ""
	// TODO runtimeHandler
	pretty_podSandboxConfig, _ := json.MarshalIndent(podSandboxConfig, "", "\t")
	fmt.Printf("======>kuberuntime manager sandbox config: %v\n", string(pretty_podSandboxConfig))
	podSandBoxID, err := m.runtimeService.RunPodSandbox(podSandboxConfig, runtimeHandler)
	if err != nil {
		message := fmt.Sprintf("CreatePodSandbox for pod %q failed: %v", format.Pod(pod), err)
		klog.Error(message)
		return "", message, err
	}

	return podSandBoxID, "", nil
}

func (m *kubeGenericRuntimeManager) generatePodSandboxConfig(pod *v1.Pod, attempt uint32) (*runtimeapi.PodSandboxConfig, error) {
	podUID := string(pod.UID)
	podSandboxConfig := &runtimeapi.PodSandboxConfig{
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Uid:       podUID,
			Attempt:   attempt,
		},
		Labels:      newPodLabels(pod),
		//Annotations: newPodAnnotations(pod),
	}
	// TODO dnsConfig


	logDir := BuildPodLogsDirectory(pod.Namespace, pod.Name, pod.UID)
	podSandboxConfig.LogDirectory = logDir

	// TODO portmapping and linux config

	return podSandboxConfig, nil
}

// determinePodSandboxIP determines the IP address of the given pod sandbox.
func (m *kubeGenericRuntimeManager) determinePodSandboxIP(podNamespace, podName string, podSandbox *runtimeapi.PodSandboxStatus) string {
	if podSandbox.Network == nil {
		klog.Warningf("Pod Sandbox status doesn't have network information, cannot report IP")
		return ""
	}
	ip := podSandbox.Network.Ip
	if len(ip) != 0 && net.ParseIP(ip) == nil {
		// ip could be an empty string if runtime is not responsible for the
		// IP (e.g., host networking).
		klog.Warningf("Pod Sandbox reported an unparseable IP %v", ip)
		return ""
	}
	return ip
}

// getPodSandboxID gets the sandbox id by podUID and returns ([]sandboxID, error).
// Param state could be nil in order to get all sandboxes belonging to same pod.
func (m *kubeGenericRuntimeManager) getSandboxIDByPodUID(podUID kubetypes.UID, state *runtimeapi.PodSandboxState) ([]string, error) {
	filter := &runtimeapi.PodSandboxFilter{
		LabelSelector: map[string]string{types.KubernetesPodUIDLabel: string(podUID)},
	}
	if state != nil {
		filter.State = &runtimeapi.PodSandboxStateValue{
			State: *state,
		}
	}
	sandboxes, err := m.runtimeService.ListPodSandbox(filter)
	if err != nil {
		klog.Errorf("ListPodSandbox with pod UID %q failed: %v", podUID, err)
		return nil, err
	}

	if len(sandboxes) == 0 {
		return nil, nil
	}

	// Sort with newest first.
	sandboxIDs := make([]string, len(sandboxes))
	// TODO sort sandbox
	sort.Sort(podSandboxByCreated(sandboxes))
	for i, s := range sandboxes {
		sandboxIDs[i] = s.Id
	}

	return sandboxIDs, nil
}