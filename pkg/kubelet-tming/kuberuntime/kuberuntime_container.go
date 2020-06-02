package kuberuntime


import (
	"k8s.io/klog"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"sort"
	kubetypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet-tming/types"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	"time"
	v1 "k8s.io/api/core/v1"
	"fmt"
	"math/rand"
	"k8s.io/kubernetes/pkg/util/tail"
	"os"
	"github.com/armon/circbuf"
	"context"
)


func (m *kubeGenericRuntimeManager) getKubeletContainers(allContainers bool) ([]*runtimeapi.Container, error) {
	filter := &runtimeapi.ContainerFilter{}

	if !allContainers {
		filter.State = &runtimeapi.ContainerStateValue{
			State:		runtimeapi.ContainerState_CONTAINER_RUNNING,
		}
	}

	containers, err := m.runtimeService.ListContainers(filter)
	if err != nil {
		klog.Errorf("getKubeletContainers failed: %v", err)
		return nil, err
	}

	return containers, nil
}

// getPodContainerStatuses gets all containers' statuses for the pod.
func (m *kubeGenericRuntimeManager) getPodContainerStatuses(uid kubetypes.UID, name, namespace string) ([]*kubecontainer.ContainerStatus, error) {
	// Select all containers of the given pod.
	containers, err := m.runtimeService.ListContainers(&runtimeapi.ContainerFilter{
		LabelSelector: map[string]string{types.KubernetesPodUIDLabel: string(uid)},
	})
	if err != nil {
		klog.Errorf("ListContainers error: %v", err)
		return nil, err
	}

	statuses := make([]*kubecontainer.ContainerStatus, len(containers))
	// TODO: optimization: set maximum number of containers per container name to examine.
	for i, c := range containers {
		status, err := m.runtimeService.ContainerStatus(c.Id)
		if err != nil {
			// Merely log this here; GetPodStatus will actually report the error out.
			klog.V(4).Infof("ContainerStatus for %s error: %v", c.Id, err)
			return nil, err
		}
		cStatus := toKubeContainerStatus(status, m.runtimeName)
		if status.State == runtimeapi.ContainerState_CONTAINER_EXITED {
			// Populate the termination message if needed.
			annotatedInfo := getContainerInfoFromAnnotations(status.Annotations)
			fallbackToLogs := annotatedInfo.TerminationMessagePolicy == v1.TerminationMessageFallbackToLogsOnError && cStatus.ExitCode != 0
			tMessage, checkLogs := getTerminationMessage(status, annotatedInfo.TerminationMessagePath, fallbackToLogs)
			if checkLogs {
				// if dockerLegacyService is populated, we're supposed to use it to fetch logs
				if m.legacyLogProvider != nil {
					tMessage, err = m.legacyLogProvider.GetContainerLogTail(uid, name, namespace, kubecontainer.ContainerID{Type: m.runtimeName, ID: c.Id})
					if err != nil {
						tMessage = fmt.Sprintf("Error reading termination message from logs: %v", err)
					}
				} else {
					tMessage = m.readLastStringFromContainerLogs(status.GetLogPath())
				}
			}
			// Use the termination message written by the application is not empty
			if len(tMessage) != 0 {
				cStatus.Message = tMessage
			}
		}
		statuses[i] = cStatus
	}

	sort.Sort(containerStatusByCreated(statuses))
	return statuses, nil
}

// readLastStringFromContainerLogs attempts to read up to the max log length from the end of the CRI log represented
// by path. It reads up to max log lines.
func (m *kubeGenericRuntimeManager) readLastStringFromContainerLogs(path string) string {
	value := int64(kubecontainer.MaxContainerTerminationMessageLogLines)
	buf, _ := circbuf.NewBuffer(kubecontainer.MaxContainerTerminationMessageLogLength)
	if err := m.ReadLogs(context.Background(), path, "", &v1.PodLogOptions{TailLines: &value}, buf, buf); err != nil {
		return fmt.Sprintf("Error on reading termination message from logs: %v", err)
	}
	return buf.String()
}


func toKubeContainerStatus(status *runtimeapi.ContainerStatus, runtimeName string) *kubecontainer.ContainerStatus {
	annotatedInfo := getContainerInfoFromAnnotations(status.Annotations)
	labeledInfo := getContainerInfoFromLabels(status.Labels)
	cStatus := &kubecontainer.ContainerStatus{
		ID: kubecontainer.ContainerID{
			Type: runtimeName,
			ID:   status.Id,
		},
		Name:         labeledInfo.ContainerName,
		Image:        status.Image.Image,
		ImageID:      status.ImageRef,
		Hash:         annotatedInfo.Hash,
		RestartCount: annotatedInfo.RestartCount,
		State:        toKubeContainerState(status.State),
		CreatedAt:    time.Unix(0, status.CreatedAt),
	}

	if status.State != runtimeapi.ContainerState_CONTAINER_CREATED {
		// If container is not in the created state, we have tried and
		// started the container. Set the StartedAt time.
		cStatus.StartedAt = time.Unix(0, status.StartedAt)
	}
	if status.State == runtimeapi.ContainerState_CONTAINER_EXITED {
		cStatus.Reason = status.Reason
		cStatus.Message = status.Message
		cStatus.ExitCode = int(status.ExitCode)
		cStatus.FinishedAt = time.Unix(0, status.FinishedAt)
	}
	return cStatus
}

// makeUID returns a randomly generated string.
func makeUID() string {
	return fmt.Sprintf("%08x", rand.Uint32())
}

// getTerminationMessage looks on the filesystem for the provided termination message path, returning a limited
// amount of those bytes, or returns true if the logs should be checked.
func getTerminationMessage(status *runtimeapi.ContainerStatus, terminationMessagePath string, fallbackToLogs bool) (string, bool) {
	if len(terminationMessagePath) == 0 {
		return "", fallbackToLogs
	}
	for _, mount := range status.Mounts {
		if mount.ContainerPath != terminationMessagePath {
			continue
		}
		path := mount.HostPath
		data, _, err := tail.ReadAtMost(path, kubecontainer.MaxContainerTerminationMessageLength)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fallbackToLogs
			}
			return fmt.Sprintf("Error on reading termination log %s: %v", path, err), false
		}
		return string(data), (fallbackToLogs && len(data) == 0)
	}
	return "", fallbackToLogs
}
