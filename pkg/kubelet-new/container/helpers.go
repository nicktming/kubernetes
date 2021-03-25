package container

import (
	"k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	hashutil "k8s.io/kubernetes/pkg/util/hash"
	"k8s.io/kubernetes/third_party/forked/golang/expansion"
	"hash/fnv"
)


// RuntimeHelper wraps kubelet to make container runtime
// able to get necessary informations like the RunContainerOptions, DNS settings, Host IP.
type RuntimeHelper interface {
	//GenerateRunContainerOptions(pod *v1.Pod, container *v1.Container, podIP string) (contOpts *RunContainerOptions, cleanupAction func(), err error)
	//GetPodDNS(pod *v1.Pod) (dnsConfig *runtimeapi.DNSConfig, err error)
	// GetPodCgroupParent returns the CgroupName identifier, and its literal cgroupfs form on the host
	// of a pod.
	GetPodCgroupParent(pod *v1.Pod) string
	//GetPodDir(podUID types.UID) string
	//GeneratePodHostNameAndDomain(pod *v1.Pod) (hostname string, hostDomain string, err error)
	//// GetExtraSupplementalGroupsForPod returns a list of the extra
	//// supplemental groups for the Pod. These extra supplemental groups come
	//// from annotations on persistent volumes that the pod depends on.
	//GetExtraSupplementalGroupsForPod(pod *v1.Pod) []int64
}

// Pod must not be nil.
func IsHostNetworkPod(pod *v1.Pod) bool {
	return pod.Spec.HostNetwork
}

func SandboxToContainerState(state runtimeapi.PodSandboxState) ContainerState {
	switch state {
	case runtimeapi.PodSandboxState_SANDBOX_READY:
		return ContainerStateRunning
	case runtimeapi.PodSandboxState_SANDBOX_NOTREADY:
		return ContainerStateExited
	}
	return ContainerStateUnknown
}

// HashContainer returns the hash of the container. It is used to compare
// the running container with its desired spec.
func HashContainer(container *v1.Container) uint64 {
	hash := fnv.New32a()
	hashutil.DeepHashObject(hash, *container)
	return uint64(hash.Sum32())
}


// GetContainerSpec gets the container spec by containerName.
func GetContainerSpec(pod *v1.Pod, containerName string) *v1.Container {
	for i, c := range pod.Spec.Containers {
		if containerName == c.Name {
			return &pod.Spec.Containers[i]
		}
	}
	for i, c := range pod.Spec.InitContainers {
		if containerName == c.Name {
			return &pod.Spec.InitContainers[i]
		}
	}
	return nil
}

func ConvertPodStatusToRunningPod(runtimeName string, podStatus *PodStatus) Pod {
	runningPod := Pod {
		ID: 		podStatus.ID,
		Name: 		podStatus.Name,
		Namespace: 	podStatus.Namespace,
	}
	for _, containerStatus := range podStatus.ContainerStatuses {
		if containerStatus.State != ContainerStateRunning {
			continue
		}
		container := &Container {
			ID: 		containerStatus.ID,
			Name: 		containerStatus.Name,
			Image: 		containerStatus.Image,
			ImageID: 	containerStatus.ImageID,
			Hash: 		containerStatus.Hash,
			State:		containerStatus.State,
		}
		runningPod.Containers = append(runningPod.Containers, container)
	}
	for _, sandbox := range podStatus.SandboxStatuses {
		runningPod.Sandboxes = append(runningPod.Sandboxes, &Container{
			ID: 		ContainerID{Type: runtimeName, ID: sandbox.Id},
			State: 		SandboxToContainerState(sandbox.State),
		})
	}
	return runningPod
}

// V1EnvVarsToMap constructs a map of environment name to value from a slice
// of env vars.
func V1EnvVarsToMap(envs []v1.EnvVar) map[string]string {
	result := map[string]string{}
	for _, env := range envs {
		result[env.Name] = env.Value
	}

	return result
}


// ExpandContainerCommandOnlyStatic substitutes only static environment variable values from the
// container environment definitions. This does *not* include valueFrom substitutions.
// TODO: callers should use ExpandContainerCommandAndArgs with a fully resolved list of environment.
func ExpandContainerCommandOnlyStatic(containerCommand []string, envs []v1.EnvVar) (command []string) {
	mapping := expansion.MappingFuncFor(V1EnvVarsToMap(envs))
	if len(containerCommand) != 0 {
		for _, cmd := range containerCommand {
			command = append(command, expansion.Expand(cmd, mapping))
		}
	}
	return command
}


