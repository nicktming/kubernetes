package kuberuntime

import (
	kubetypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet-tming/types"
	"k8s.io/api/core/v1"

	"encoding/json"
	"strconv"
	//kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	//"k8s.io/kubernetes/pkg/kubelet/util/format"

)


const (
	// TODO: change those label names to follow kubernetes's format
	podDeletionGracePeriodLabel    = "io.kubernetes.pod.deletionGracePeriod"
	podTerminationGracePeriodLabel = "io.kubernetes.pod.terminationGracePeriod"

	containerHashLabel                     = "io.kubernetes.container.hash"
	containerRestartCountLabel             = "io.kubernetes.container.restartCount"
	containerTerminationMessagePathLabel   = "io.kubernetes.container.terminationMessagePath"
	containerTerminationMessagePolicyLabel = "io.kubernetes.container.terminationMessagePolicy"
	containerPreStopHandlerLabel           = "io.kubernetes.container.preStopHandler"
	containerPortsLabel                    = "io.kubernetes.container.ports"
)

type labeledPodSandboxInfo struct {
	// Labels from v1.Pod
	Labels       map[string]string
	PodName      string
	PodNamespace string
	PodUID       kubetypes.UID
}

type annotatedPodSandboxInfo struct {
	// Annotations from v1.Pod
	Annotations map[string]string
}

type labeledContainerInfo struct {
	ContainerName string
	PodName       string
	PodNamespace  string
	PodUID        kubetypes.UID
}

type annotatedContainerInfo struct {
	Hash                      uint64
	RestartCount              int
	PodDeletionGracePeriod    *int64
	PodTerminationGracePeriod *int64
	TerminationMessagePath    string
	TerminationMessagePolicy  v1.TerminationMessagePolicy
	PreStopHandler            *v1.Handler
	ContainerPorts            []v1.ContainerPort
}

// newPodLabels creates pod labels from v1.Pod.
func newPodLabels(pod *v1.Pod) map[string]string {
	labels := map[string]string{}

	// Get labels from v1.Pod
	for k, v := range pod.Labels {
		labels[k] = v
	}

	labels[types.KubernetesPodNameLabel] = pod.Name
	labels[types.KubernetesPodNamespaceLabel] = pod.Namespace
	labels[types.KubernetesPodUIDLabel] = string(pod.UID)

	return labels
}

// newPodAnnotations creates pod annotations from v1.Pod.
func newPodAnnotations(pod *v1.Pod) map[string]string {
	return pod.Annotations
}

// newContainerLabels creates container labels from v1.Container and v1.Pod.
func newContainerLabels(container *v1.Container, pod *v1.Pod) map[string]string {
	labels := map[string]string{}
	labels[types.KubernetesPodNameLabel] = pod.Name
	labels[types.KubernetesPodNamespaceLabel] = pod.Namespace
	labels[types.KubernetesPodUIDLabel] = string(pod.UID)
	labels[types.KubernetesContainerNameLabel] = container.Name

	return labels
}


// getPodSandboxInfoFromLabels gets labeledPodSandboxInfo from labels.
func getPodSandboxInfoFromLabels(labels map[string]string) *labeledPodSandboxInfo {
	podSandboxInfo := &labeledPodSandboxInfo{
		Labels:       make(map[string]string),
		PodName:      getStringValueFromLabel(labels, types.KubernetesPodNameLabel),
		PodNamespace: getStringValueFromLabel(labels, types.KubernetesPodNamespaceLabel),
		PodUID:       kubetypes.UID(getStringValueFromLabel(labels, types.KubernetesPodUIDLabel)),
	}

	// Remain only labels from v1.Pod
	for k, v := range labels {
		if k != types.KubernetesPodNameLabel && k != types.KubernetesPodNamespaceLabel && k != types.KubernetesPodUIDLabel {
			podSandboxInfo.Labels[k] = v
		}
	}

	return podSandboxInfo
}

// getPodSandboxInfoFromAnnotations gets annotatedPodSandboxInfo from annotations.
func getPodSandboxInfoFromAnnotations(annotations map[string]string) *annotatedPodSandboxInfo {
	return &annotatedPodSandboxInfo{
		Annotations: annotations,
	}
}

// getContainerInfoFromLabels gets labeledContainerInfo from labels.
func getContainerInfoFromLabels(labels map[string]string) *labeledContainerInfo {
	return &labeledContainerInfo{
		PodName:       getStringValueFromLabel(labels, types.KubernetesPodNameLabel),
		PodNamespace:  getStringValueFromLabel(labels, types.KubernetesPodNamespaceLabel),
		PodUID:        kubetypes.UID(getStringValueFromLabel(labels, types.KubernetesPodUIDLabel)),
		ContainerName: getStringValueFromLabel(labels, types.KubernetesContainerNameLabel),
	}
}

// getContainerInfoFromAnnotations gets annotatedContainerInfo from annotations.
func getContainerInfoFromAnnotations(annotations map[string]string) *annotatedContainerInfo {
	var err error
	containerInfo := &annotatedContainerInfo{
		TerminationMessagePath:   getStringValueFromLabel(annotations, containerTerminationMessagePathLabel),
		TerminationMessagePolicy: v1.TerminationMessagePolicy(getStringValueFromLabel(annotations, containerTerminationMessagePolicyLabel)),
	}

	if containerInfo.Hash, err = getUint64ValueFromLabel(annotations, containerHashLabel); err != nil {
		klog.Errorf("Unable to get %q from annotations %q: %v", containerHashLabel, annotations, err)
	}
	if containerInfo.RestartCount, err = getIntValueFromLabel(annotations, containerRestartCountLabel); err != nil {
		klog.Errorf("Unable to get %q from annotations %q: %v", containerRestartCountLabel, annotations, err)
	}
	if containerInfo.PodDeletionGracePeriod, err = getInt64PointerFromLabel(annotations, podDeletionGracePeriodLabel); err != nil {
		klog.Errorf("Unable to get %q from annotations %q: %v", podDeletionGracePeriodLabel, annotations, err)
	}
	if containerInfo.PodTerminationGracePeriod, err = getInt64PointerFromLabel(annotations, podTerminationGracePeriodLabel); err != nil {
		klog.Errorf("Unable to get %q from annotations %q: %v", podTerminationGracePeriodLabel, annotations, err)
	}

	preStopHandler := &v1.Handler{}
	if found, err := getJSONObjectFromLabel(annotations, containerPreStopHandlerLabel, preStopHandler); err != nil {
		klog.Errorf("Unable to get %q from annotations %q: %v", containerPreStopHandlerLabel, annotations, err)
	} else if found {
		containerInfo.PreStopHandler = preStopHandler
	}

	containerPorts := []v1.ContainerPort{}
	if found, err := getJSONObjectFromLabel(annotations, containerPortsLabel, &containerPorts); err != nil {
		klog.Errorf("Unable to get %q from annotations %q: %v", containerPortsLabel, annotations, err)
	} else if found {
		containerInfo.ContainerPorts = containerPorts
	}

	return containerInfo
}

func getStringValueFromLabel(labels map[string]string, label string) string {
	if value, found := labels[label]; found {
		return value
	}
	// Do not report error, because there should be many old containers without label now.
	klog.V(3).Infof("Container doesn't have label %s, it may be an old or invalid container", label)
	// Return empty string "" for these containers, the caller will get value by other ways.
	return ""
}

func getIntValueFromLabel(labels map[string]string, label string) (int, error) {
	if strValue, found := labels[label]; found {
		intValue, err := strconv.Atoi(strValue)
		if err != nil {
			// This really should not happen. Just set value to 0 to handle this abnormal case
			return 0, err
		}
		return intValue, nil
	}
	// Do not report error, because there should be many old containers without label now.
	klog.V(3).Infof("Container doesn't have label %s, it may be an old or invalid container", label)
	// Just set the value to 0
	return 0, nil
}

func getUint64ValueFromLabel(labels map[string]string, label string) (uint64, error) {
	if strValue, found := labels[label]; found {
		intValue, err := strconv.ParseUint(strValue, 16, 64)
		if err != nil {
			// This really should not happen. Just set value to 0 to handle this abnormal case
			return 0, err
		}
		return intValue, nil
	}
	// Do not report error, because there should be many old containers without label now.
	klog.V(3).Infof("Container doesn't have label %s, it may be an old or invalid container", label)
	// Just set the value to 0
	return 0, nil
}

func getInt64PointerFromLabel(labels map[string]string, label string) (*int64, error) {
	if strValue, found := labels[label]; found {
		int64Value, err := strconv.ParseInt(strValue, 10, 64)
		if err != nil {
			return nil, err
		}
		return &int64Value, nil
	}
	// If the label is not found, return pointer nil.
	return nil, nil
}

// getJSONObjectFromLabel returns a bool value indicating whether an object is found.
func getJSONObjectFromLabel(labels map[string]string, label string, value interface{}) (bool, error) {
	if strValue, found := labels[label]; found {
		err := json.Unmarshal([]byte(strValue), value)
		return found, err
	}
	// If the label is not found, return not found.
	return false, nil
}
