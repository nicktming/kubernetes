package kuberuntime

import (
	kubetypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet-new/types"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	"k8s.io/klog"
	"k8s.io/api/core/v1"
	"strconv"
	"encoding/json"
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

type labeledContainerInfo struct {
	ContainerName string
	PodName       string
	PodNamespace  string
	PodUID        kubetypes.UID
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


func getStringValueFromLabel(labels map[string]string, label string) string {
	if value, found := labels[label]; found {
		return value
	}
	// Do not report error, because there should be many old containers without label now.
	klog.V(3).Infof("Container doesn't have label %s, it may be an old or invalid container", label)
	// Return empty string "" for these containers, the caller will get value by other ways.
	return ""
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

func newPodLabels(pod *v1.Pod) map[string]string {
	labels := map[string]string{}
	labels[types.KubernetesPodUIDLabel]= string(pod.UID)
	return labels
}

// newContainerAnnotations creates container annotations from v1.Container and v1.Pod.
// TODO  opts *kubecontainer.RunContainerOptions
func newContainerAnnotations(container *v1.Container, pod *v1.Pod, restartCount int) map[string]string {
	annotations := map[string]string{}

	// Kubelet always overrides device plugin annotations if they are conflicting
	//for _, a := range opts.Annotations {
	//	annotations[a.Name] = a.Value
	//}

	annotations[containerHashLabel] = strconv.FormatUint(kubecontainer.HashContainer(container), 16)
	annotations[containerRestartCountLabel] = strconv.Itoa(restartCount)
	annotations[containerTerminationMessagePathLabel] = container.TerminationMessagePath
	annotations[containerTerminationMessagePolicyLabel] = string(container.TerminationMessagePolicy)

	if pod.DeletionGracePeriodSeconds != nil {
		annotations[podDeletionGracePeriodLabel] = strconv.FormatInt(*pod.DeletionGracePeriodSeconds, 10)
	}
	if pod.Spec.TerminationGracePeriodSeconds != nil {
		annotations[podTerminationGracePeriodLabel] = strconv.FormatInt(*pod.Spec.TerminationGracePeriodSeconds, 10)
	}

	if container.Lifecycle != nil && container.Lifecycle.PreStop != nil {
		// Using json encoding so that the PreStop handler object is readable after writing as a label
		rawPreStop, err := json.Marshal(container.Lifecycle.PreStop)
		if err != nil {
			klog.Errorf("Unable to marshal lifecycle PreStop handler for container %q of pod %q: %v", container.Name, format.Pod(pod), err)
		} else {
			annotations[containerPreStopHandlerLabel] = string(rawPreStop)
		}
	}

	if len(container.Ports) > 0 {
		rawContainerPorts, err := json.Marshal(container.Ports)
		if err != nil {
			klog.Errorf("Unable to marshal container ports for container %q for pod %q: %v", container.Name, format.Pod(pod), err)
		} else {
			annotations[containerPortsLabel] = string(rawContainerPorts)
		}
	}

	return annotations
}
