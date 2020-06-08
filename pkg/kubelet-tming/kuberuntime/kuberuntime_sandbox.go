package kuberuntime

import (
	"fmt"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog"
	"sort"
	kubetypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet-tming/types"
	"k8s.io/api/core/v1"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
)

// createPodSandbox creates a pod sandbox and returns (podSandBoxID, message, error).
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

	// TODO runtimeClassManager
	runtimeHandler := ""
	//if utilfeature.DefaultFeatureGate.Enabled(features.RuntimeClass) && m.runtimeClassManager != nil {
	//	runtimeHandler, err = m.runtimeClassManager.LookupRuntimeHandler(pod.Spec.RuntimeClassName)
	//	if err != nil {
	//		message := fmt.Sprintf("CreatePodSandbox for pod %q failed: %v", format.Pod(pod), err)
	//		return "", message, err
	//	}
	//	if runtimeHandler != "" {
	//		klog.V(2).Infof("Running pod %s with RuntimeHandler %q", format.Pod(pod), runtimeHandler)
	//	}
	//}

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
	podSandboxConfig := &runtimeapi.PodSandboxConfig {
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name: 			pod.Name,
			Namespace: 		pod.Namespace,
			Uid:			podUID,
			Attempt: 		attempt,
		},
		Labels: 		newPodLabels(pod),
		Annotations: 		newPodAnnotations(pod),
	}
	// TODO runtimeHelper

	//dnsConfig, err := m.runtimeHelper.GetPodDNS(pod)
	//if err != nil {
	//	return nil, err
	//}
	//podSandboxConfig.DnsConfig = dnsConfig

	//if !kubecontainer.IsHostNetworkPod(pod) {
	//	// TODO: Add domain support in new runtime interface
	//	hostname, _, err := m.runtimeHelper.GeneratePodHostNameAndDomain(pod)
	//	if err != nil {
	//		return nil, err
	//	}
	//	podSandboxConfig.Hostname = hostname
	//}

	logDir := BuildPodLogsDirectory(pod.Namespace, pod.Name, pod.UID)
	podSandboxConfig.LogDirectory = logDir

	portMappings := []*runtimeapi.PortMapping{}
	for _, c := range pod.Spec.Containers {
		containerPortMappings := kubecontainer.MakePortMappings(&c)

		for idx := range containerPortMappings {
			port := containerPortMappings[idx]
			hostPort := int32(port.HostPort)
			containerPort := int32(port.ContainerPort)
			protocol := toRuntimeProtocol(port.Protocol)
			portMappings = append(portMappings, &runtimeapi.PortMapping{
				HostIp:        port.HostIP,
				HostPort:      hostPort,
				ContainerPort: containerPort,
				Protocol:      protocol,
			})
		}

	}

	if len(portMappings) > 0 {
		podSandboxConfig.PortMappings = portMappings
	}

	lc, err := m.generatePodSandboxLinuxConfig(pod)
	if err != nil {
		return nil, err
	}

	podSandboxConfig.Linux = lc

	return podSandboxConfig, nil
}


func (m *kubeGenericRuntimeManager) generatePodSandboxLinuxConfig(pod *v1.Pod) (*runtimeapi.LinuxPodSandboxConfig, error) {
	// TODO

	cgroupParent := m.runtimeHelper.GetPodCgroupParent(pod)

	klog.Infof("generatePodSandboxLinuxConfig cgroupParent: %v", cgroupParent)

	lc := &runtimeapi.LinuxPodSandboxConfig{
		CgroupParent: cgroupParent,
		SecurityContext: &runtimeapi.LinuxSandboxSecurityContext{
			Privileged:         kubecontainer.HasPrivilegedContainer(pod),
			SeccompProfilePath: m.getSeccompProfileFromAnnotations(pod.Annotations, ""),
		},
	}

	sysctls := make(map[string]string)
	if utilfeature.DefaultFeatureGate.Enabled(features.Sysctls) {
		if pod.Spec.SecurityContext != nil {
			for _, c := range pod.Spec.SecurityContext.Sysctls {
				sysctls[c.Name] = c.Value
			}
		}
	}

	lc.Sysctls = sysctls

	//if pod.Spec.SecurityContext != nil {
	//	sc := pod.Spec.SecurityContext
	//	if sc.RunAsUser != nil {
	//		lc.SecurityContext.RunAsUser = &runtimeapi.Int64Value{Value: int64(*sc.RunAsUser)}
	//	}
	//	if sc.RunAsGroup != nil {
	//		lc.SecurityContext.RunAsGroup = &runtimeapi.Int64Value{Value: int64(*sc.RunAsGroup)}
	//	}
	//	lc.SecurityContext.NamespaceOptions = namespacesForPod(pod)
	//
	//	if sc.FSGroup != nil {
	//		lc.SecurityContext.SupplementalGroups = append(lc.SecurityContext.SupplementalGroups, int64(*sc.FSGroup))
	//	}
	//	if groups := m.runtimeHelper.GetExtraSupplementalGroupsForPod(pod); len(groups) > 0 {
	//		lc.SecurityContext.SupplementalGroups = append(lc.SecurityContext.SupplementalGroups, groups...)
	//	}
	//	if sc.SupplementalGroups != nil {
	//		for _, sg := range sc.SupplementalGroups {
	//			lc.SecurityContext.SupplementalGroups = append(lc.SecurityContext.SupplementalGroups, int64(sg))
	//		}
	//	}
	//	if sc.SELinuxOptions != nil {
	//		lc.SecurityContext.SelinuxOptions = &runtimeapi.SELinuxOption{
	//			User:  sc.SELinuxOptions.User,
	//			Role:  sc.SELinuxOptions.Role,
	//			Type:  sc.SELinuxOptions.Type,
	//			Level: sc.SELinuxOptions.Level,
	//		}
	//	}
	//}

	return lc, nil
}


func (m *kubeGenericRuntimeManager) getKubeletSandboxes(all bool) ([]*runtimeapi.PodSandbox, error) {
	var filter *runtimeapi.PodSandboxFilter

	if !all {
		readyState := runtimeapi.PodSandboxState_SANDBOX_READY

		filter = &runtimeapi.PodSandboxFilter {
			State: 		&runtimeapi.PodSandboxStateValue{
				State:		readyState,
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
	sort.Sort(podSandboxByCreated(sandboxes))
	for i, s := range sandboxes {
		sandboxIDs[i] = s.Id
	}

	return sandboxIDs, nil
}
