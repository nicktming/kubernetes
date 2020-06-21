package kubelet_tming

import (
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet/eviction"
	"k8s.io/klog"
	"sort"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/pkg/kubelet-tming/status"
	v1qos "k8s.io/kubernetes/pkg/apis/core/v1/helper/qos"
	kubetypes "k8s.io/kubernetes/pkg/kubelet-tming/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	//"path/filepath"
	volumeutil "k8s.io/kubernetes/pkg/volume/util"
	"encoding/json"
	"fmt"
	"strings"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/kubernetes/pkg/volume/util/subpath"
	mountutil "k8s.io/kubernetes/pkg/util/mount"
	"runtime"
	"path/filepath"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

// findContainer finds and returns the container with the given pod ID, full name, and container name.
// It returns nil if not found.
func (kl *Kubelet) findContainer(podFullName string, podUID types.UID, containerName string) (*kubecontainer.Container, error) {
	pods, err := kl.containerRuntime.GetPods(false)
	if err != nil {
		return nil, err
	}
	// Resolve and type convert back again.
	// We need the static pod UID but the kubecontainer API works with types.UID.
	podUID = types.UID(kl.podManager.TranslatePodUID(podUID))
	pod := kubecontainer.Pods(pods).FindPod(podFullName, podUID)
	return pod.FindContainerByName(containerName), nil
}


// RunInContainer runs a command in a container, returns the combined stdout, stderr as an array of bytes
func (kl *Kubelet) RunInContainer(podFullName string, podUID types.UID, containerName string, cmd []string) ([]byte, error) {
	container, err := kl.findContainer(podFullName, podUID, containerName)
	if err != nil {
		return nil, err
	}
	if container == nil {
		return nil, fmt.Errorf("container not found (%q)", containerName)
	}
	// TODO(tallclair): Pass a proper timeout value.
	return kl.runner.RunInContainer(container.ID, cmd, 0)
}

// GetPodCgroupParent gets pod cgroup parent from container manager.
func (kl *Kubelet) GetPodCgroupParent(pod *v1.Pod) string {
	pcm := kl.containerManager.NewPodContainerManager()
	_, cgroupParent := pcm.GetPodContainerName(pod)
	return cgroupParent
}

// truncatePodHostnameIfNeeded truncates the pod hostname if it's longer than 63 chars.
func truncatePodHostnameIfNeeded(podName, hostname string) (string, error) {
	// Cap hostname at 63 chars (specification is 64bytes which is 63 chars and the null terminating char).
	const hostnameMaxLen = 63
	if len(hostname) <= hostnameMaxLen {
		return hostname, nil
	}
	truncated := hostname[:hostnameMaxLen]
	klog.Errorf("hostname for pod:%q was longer than %d. Truncated hostname to :%q", podName, hostnameMaxLen, truncated)
	// hostname should not end with '-' or '.'
	truncated = strings.TrimRight(truncated, "-.")
	if len(truncated) == 0 {
		// This should never happen.
		return "", fmt.Errorf("hostname for pod %q was invalid: %q", podName, hostname)
	}
	return truncated, nil
}


// GeneratePodHostNameAndDomain creates a hostname and domain name for a pod,
// given that pod's spec and annotations or returns an error.
func (kl *Kubelet) GeneratePodHostNameAndDomain(pod *v1.Pod) (string, string, error) {
	//clusterDomain := kl.dnsConfigurer.ClusterDomain

	clusterDomain := "cluster.local"
	hostname := pod.Name
	if len(pod.Spec.Hostname) > 0 {
		if msgs := utilvalidation.IsDNS1123Label(pod.Spec.Hostname); len(msgs) != 0 {
			return "", "", fmt.Errorf("Pod Hostname %q is not a valid DNS label: %s", pod.Spec.Hostname, strings.Join(msgs, ";"))
		}
		hostname = pod.Spec.Hostname
	}

	hostname, err := truncatePodHostnameIfNeeded(pod.Name, hostname)
	if err != nil {
		return "", "", err
	}

	hostDomain := ""
	if len(pod.Spec.Subdomain) > 0 {
		if msgs := utilvalidation.IsDNS1123Label(pod.Spec.Subdomain); len(msgs) != 0 {
			return "", "", fmt.Errorf("Pod Subdomain %q is not a valid DNS label: %s", pod.Spec.Subdomain, strings.Join(msgs, ";"))
		}
		hostDomain = fmt.Sprintf("%s.%s.svc.%s", pod.Spec.Subdomain, pod.Namespace, clusterDomain)
	}

	return hostname, hostDomain, nil
}

// GenerateRunContainerOptions generates the RunContainerOptions, which can be used by
// the container runtime to set parameters for launching a container.

func (kl *Kubelet) GenerateRunContainerOptions(pod *v1.Pod, container *v1.Container, podIP string) (*kubecontainer.RunContainerOptions, func(), error) {
	opts, err := kl.containerManager.GetResources(pod, container)
	if err != nil {
		return nil, nil, err
	}
	hostname, hostDomainName, err := kl.GeneratePodHostNameAndDomain(pod)
	if err != nil {
		return nil, nil, err
	}
	opts.Hostname = hostname
	podName := volumeutil.GetUniquePodName(pod)
	volumes := kl.volumeManager.GetMountedVolumesForPod(podName)

	opts.PortMappings = kubecontainer.MakePortMappings(container)

	// TODO: remove feature gate check after no longer needed
	//if utilfeature.DefaultFeatureGate.Enabled(features.BlockVolume) {
	//	blkutil := volumepathhandler.NewBlockVolumePathHandler()
	//	blkVolumes, err := kl.makeBlockVolumes(pod, container, volumes, blkutil)
	//	if err != nil {
	//		return nil, nil, err
	//	}
	//	opts.Devices = append(opts.Devices, blkVolumes...)
	//}

	// TODO env
	//envs, err := kl.makeEnvironmentVariables(pod, container, podIP)
	//if err != nil {
	//	return nil, nil, err
	//}
	//opts.Envs = append(opts.Envs, envs...)

	// TODO mount

	mounts, cleanupAction, err := makeMounts(pod, kl.getPodDir(pod.UID), container, hostname, hostDomainName, podIP, volumes, kl.mounter, kl.subpather, opts.Envs)
	if err != nil {
		return nil, cleanupAction, err
	}
	opts.Mounts = append(opts.Mounts, mounts...)

	klog.Infof("==========>GenerateRunContainerOptions container.TerminationMessagePath:%v!", container.TerminationMessagePath)

	if len(container.TerminationMessagePath) != 0 && runtime.GOOS != "windows" {
		p := kl.getPodContainerDir(pod.UID, container.Name)
		if err := os.MkdirAll(p, 0750); err != nil {
			klog.Errorf("Error on creating %q: %v", p, err)
		} else {
			opts.PodContainerDir = p
		}
	}

	// only do this check if the experimental behavior is enabled, otherwise allow it to default to false
	//if kl.experimentalHostUserNamespaceDefaulting {
	//	opts.EnableHostUserNamespace = kl.enableHostUserNamespace(pod)
	//}

	return opts, cleanupAction, nil
}

// makeMounts determines the mount points for the given container.
func makeMounts(pod *v1.Pod, podDir string, container *v1.Container, hostName, hostDomain, podIP string, podVolumes kubecontainer.VolumeMap, mounter mountutil.Interface, subpather subpath.Interface, expandEnvs []kubecontainer.EnvVar) ([]kubecontainer.Mount, func(), error) {
	// Kubernetes only mounts on /etc/hosts if:
	// - container is not an infrastructure (pause) container
	// - container is not already mounting on /etc/hosts
	// - OS is not Windows
	// Kubernetes will not mount /etc/hosts if:
	// - when the Pod sandbox is being created, its IP is still unknown. Hence, PodIP will not have been set.
	mountEtcHostsFile := len(podIP) > 0 && runtime.GOOS != "windows"
	klog.Infof("container: %v/%v/%v podIP: %q creating hosts mount: %v", pod.Namespace, pod.Name, container.Name, podIP, mountEtcHostsFile)
	mounts := []kubecontainer.Mount{}
	var cleanupAction func()
	//for i, mount := range container.VolumeMounts {
	for _, mount := range container.VolumeMounts {
		// do not mount /etc/hosts if container is already mounting on the path
		mountEtcHostsFile = mountEtcHostsFile && (mount.MountPath != etcHostsPath)
		vol, ok := podVolumes[mount.Name]
		if !ok || vol.Mounter == nil {
			klog.Errorf("Mount cannot be satisfied for container %q, because the volume is missing or the volume mounter is nil: %+v", container.Name, mount)
			return nil, cleanupAction, fmt.Errorf("cannot find volume %q to mount into container %q", mount.Name, container.Name)
		}
		relabelVolume := false
		// If the volume supports SELinux and it has not been
		// relabeled already and it is not a read-only volume,
		// relabel it and mark it as labeled
		if vol.Mounter.GetAttributes().Managed && vol.Mounter.GetAttributes().SupportsSELinux && !vol.SELinuxLabeled {
			vol.SELinuxLabeled = true
			relabelVolume = true
		}
		hostPath, err := volumeutil.GetPath(vol.Mounter)
		if err != nil {
			return nil, cleanupAction, err
		}

		// TODO subpath
		//subPath := mount.SubPath
		//if mount.SubPathExpr != "" {
		//	if !utilfeature.DefaultFeatureGate.Enabled(features.VolumeSubpath) {
		//		return nil, cleanupAction, fmt.Errorf("volume subpaths are disabled")
		//	}
		//
		//	if !utilfeature.DefaultFeatureGate.Enabled(features.VolumeSubpathEnvExpansion) {
		//		return nil, cleanupAction, fmt.Errorf("volume subpath expansion is disabled")
		//	}
		//
		//	subPath, err = kubecontainer.ExpandContainerVolumeMounts(mount, expandEnvs)
		//
		//	if err != nil {
		//		return nil, cleanupAction, err
		//	}
		//}
		//
		//if subPath != "" {
		//	if !utilfeature.DefaultFeatureGate.Enabled(features.VolumeSubpath) {
		//		return nil, cleanupAction, fmt.Errorf("volume subpaths are disabled")
		//	}
		//
		//	if filepath.IsAbs(subPath) {
		//		return nil, cleanupAction, fmt.Errorf("error SubPath `%s` must not be an absolute path", subPath)
		//	}
		//
		//	err = volumevalidation.ValidatePathNoBacksteps(subPath)
		//	if err != nil {
		//		return nil, cleanupAction, fmt.Errorf("unable to provision SubPath `%s`: %v", subPath, err)
		//	}
		//
		//	volumePath := hostPath
		//	hostPath = filepath.Join(volumePath, subPath)
		//
		//	if subPathExists, err := mounter.ExistsPath(hostPath); err != nil {
		//		klog.Errorf("Could not determine if subPath %s exists; will not attempt to change its permissions", hostPath)
		//	} else if !subPathExists {
		//		// Create the sub path now because if it's auto-created later when referenced, it may have an
		//		// incorrect ownership and mode. For example, the sub path directory must have at least g+rwx
		//		// when the pod specifies an fsGroup, and if the directory is not created here, Docker will
		//		// later auto-create it with the incorrect mode 0750
		//		// Make extra care not to escape the volume!
		//		perm, err := mounter.GetMode(volumePath)
		//		if err != nil {
		//			return nil, cleanupAction, err
		//		}
		//		if err := subpather.SafeMakeDir(subPath, volumePath, perm); err != nil {
		//			// Don't pass detailed error back to the user because it could give information about host filesystem
		//			klog.Errorf("failed to create subPath directory for volumeMount %q of container %q: %v", mount.Name, container.Name, err)
		//			return nil, cleanupAction, fmt.Errorf("failed to create subPath directory for volumeMount %q of container %q", mount.Name, container.Name)
		//		}
		//	}
		//	hostPath, cleanupAction, err = subpather.PrepareSafeSubpath(subpath.Subpath{
		//		VolumeMountIndex: i,
		//		Path:             hostPath,
		//		VolumeName:       vol.InnerVolumeSpecName,
		//		VolumePath:       volumePath,
		//		PodDir:           podDir,
		//		ContainerName:    container.Name,
		//	})
		//	if err != nil {
		//		// Don't pass detailed error back to the user because it could give information about host filesystem
		//		klog.Errorf("failed to prepare subPath for volumeMount %q of container %q: %v", mount.Name, container.Name, err)
		//		return nil, cleanupAction, fmt.Errorf("failed to prepare subPath for volumeMount %q of container %q", mount.Name, container.Name)
		//	}
		//}

		// Docker Volume Mounts fail on Windows if it is not of the form C:/
		if volumeutil.IsWindowsLocalPath(runtime.GOOS, hostPath) {
			hostPath = volumeutil.MakeAbsolutePath(runtime.GOOS, hostPath)
		}

		containerPath := mount.MountPath
		// IsAbs returns false for UNC path/SMB shares/named pipes in Windows. So check for those specifically and skip MakeAbsolutePath
		if !volumeutil.IsWindowsUNCPath(runtime.GOOS, containerPath) && !filepath.IsAbs(containerPath) {
			containerPath = volumeutil.MakeAbsolutePath(runtime.GOOS, containerPath)
		}

		// TODO propagation
		propagation, err := translateMountPropagation(mount.MountPropagation)
		if err != nil {
			return nil, cleanupAction, err
		}
		klog.Infof("Pod %q container %q mount %q has propagation %q", format.Pod(pod), container.Name, mount.Name, propagation)

		mustMountRO := vol.Mounter.GetAttributes().ReadOnly

		mounts = append(mounts, kubecontainer.Mount{
			Name:           mount.Name,
			ContainerPath:  containerPath,
			HostPath:       hostPath,
			ReadOnly:       mount.ReadOnly || mustMountRO,
			SELinuxRelabel: relabelVolume,
			Propagation:    propagation,
		})
	}
	// TODO etc/hosts mount
	//if mountEtcHostsFile {
	//	hostAliases := pod.Spec.HostAliases
	//	hostsMount, err := makeHostsMount(podDir, podIP, hostName, hostDomain, hostAliases, pod.Spec.HostNetwork)
	//	if err != nil {
	//		return nil, cleanupAction, err
	//	}
	//	mounts = append(mounts, *hostsMount)
	//}
	return mounts, cleanupAction, nil

}

// translateMountPropagation transforms v1.MountPropagationMode to
// runtimeapi.MountPropagation.
func translateMountPropagation(mountMode *v1.MountPropagationMode) (runtimeapi.MountPropagation, error) {
	if runtime.GOOS == "windows" {
		// Windows containers doesn't support mount propagation, use private for it.
		// Refer https://docs.docker.com/storage/bind-mounts/#configure-bind-propagation.
		return runtimeapi.MountPropagation_PROPAGATION_PRIVATE, nil
	}

	switch {
	case mountMode == nil:
		// PRIVATE is the default
		return runtimeapi.MountPropagation_PROPAGATION_PRIVATE, nil
	case *mountMode == v1.MountPropagationHostToContainer:
		return runtimeapi.MountPropagation_PROPAGATION_HOST_TO_CONTAINER, nil
	case *mountMode == v1.MountPropagationBidirectional:
		return runtimeapi.MountPropagation_PROPAGATION_BIDIRECTIONAL, nil
	case *mountMode == v1.MountPropagationNone:
		return runtimeapi.MountPropagation_PROPAGATION_PRIVATE, nil
	default:
		return 0, fmt.Errorf("invalid MountPropagation mode: %q", *mountMode)
	}
}

func notRunning(statuses []v1.ContainerStatus) bool {
	for _, status := range statuses {
		if status.State.Terminated == nil && status.State.Waiting == nil {
			return false
		}
	}
	return true
}


func (kl *Kubelet) makePodDataDirs(pod *v1.Pod) error {
	uid := pod.UID
	if err := os.MkdirAll(kl.getPodDir(uid), 0750); err != nil && !os.IsExist(err) {
		return err
	}

	if err := os.MkdirAll(kl.getPodVolumesDir(uid), 0750); err != nil && !os.IsExist(err) {
		return err
	}
	if err := os.MkdirAll(kl.getPodPluginsDir(uid), 0750); err != nil && !os.IsExist(err) {
		return err
	}
	return nil
}

// podIsTerminated returns true if pod is in the terminated state ("Failed" or "Succeeded").
func (kl *Kubelet) podIsTerminated(pod *v1.Pod) bool {
	// Check the cached pod status which was set after the last sync.
	status, ok := kl.statusManager.GetPodStatus(pod.UID)
	if !ok {
		// If there is no cached status, use the status from the
		// apiserver. This is useful if kubelet has recently been
		// restarted.
		status = pod.Status
	}
	return status.Phase == v1.PodFailed || status.Phase == v1.PodSucceeded || (pod.DeletionTimestamp != nil && notRunning(status.ContainerStatuses))
}

func (kl *Kubelet) IsPodTerminated(uid types.UID) bool {
	pod, podFound := kl.podManager.GetPodByUID(uid)
	if !podFound {
		return true
	}
	return kl.podIsTerminated(pod)
}


func (kl *Kubelet) IsPodDeleted(uid types.UID) bool {
	pod, podFound := kl.podManager.GetPodByUID(uid)
	if !podFound {
		return true
	}

	//status, statusFound := kl.statusManager.GetPodStatus(pod.UID)
	//if !statusFound {
		status := pod.Status
	//}

	return eviction.PodIsEvicted(status) || (pod.DeletionTimestamp != nil && notRunning(status.ContainerStatuses))
}

// generateAPIPodStatus creates the final API pod status for a pod, given the
// internal pod status.
func (kl *Kubelet) generateAPIPodStatus(pod *v1.Pod, podStatus *kubecontainer.PodStatus) v1.PodStatus {
	klog.V(3).Infof("Generating status for %q", format.Pod(pod))

	s := kl.convertStatusToAPIStatus(pod, podStatus)

	// check if an internal module has requested the pod is evicted.
	//for _, podSyncHandler := range kl.PodSyncHandlers {
	//	if result := podSyncHandler.ShouldEvict(pod); result.Evict {
	//		s.Phase = v1.PodFailed
	//		s.Reason = result.Reason
	//		s.Message = result.Message
	//		return *s
	//	}
	//}

	// Assume info is ready to process
	spec := &pod.Spec
	allStatus := append(append([]v1.ContainerStatus{}, s.ContainerStatuses...), s.InitContainerStatuses...)
	s.Phase = getPhase(spec, allStatus)

	klog.Infof("===>generateAPIPodStatus pod(%v/%v(%v)): %v", pod.Namespace, pod.Name, pod.UID, s.Phase)

	// Check for illegal phase transition
	if pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodSucceeded {
		// API server shows terminal phase; transitions are not allowed
		if s.Phase != pod.Status.Phase {
			klog.Errorf("Pod attempted illegal phase transition from %s to %s: %v", pod.Status.Phase, s.Phase, s)
			// Force back to phase from the API server
			s.Phase = pod.Status.Phase
		}
	}

	// TODO
	//kl.probeManager.UpdatePodStatus(pod.UID, s)

	// TODO Replace probeManager

	for i, _ := range s.ContainerStatuses {
		s.ContainerStatuses[i].Ready = s.ContainerStatuses[i].State.Running != nil
	}

	s.Conditions = append(s.Conditions, status.GeneratePodInitializedCondition(spec, s.InitContainerStatuses, s.Phase))
	s.Conditions = append(s.Conditions, status.GeneratePodReadyCondition(spec, s.Conditions, s.ContainerStatuses, s.Phase))
	s.Conditions = append(s.Conditions, status.GenerateContainersReadyCondition(spec, s.ContainerStatuses, s.Phase))
	// Status manager will take care of the LastTransitionTimestamp, either preserve
	// the timestamp from apiserver, or set a new one. When kubelet sees the pod,
	// `PodScheduled` condition must be true.
	s.Conditions = append(s.Conditions, v1.PodCondition{
		Type:   v1.PodScheduled,
		Status: v1.ConditionTrue,
	})

	if kl.kubeClient != nil {
		hostIP, err := kl.getHostIPAnyWay()
		if err != nil {
			klog.V(4).Infof("Cannot get host IP: %v", err)
		} else {
			s.HostIP = hostIP.String()
			if kubecontainer.IsHostNetworkPod(pod) && s.PodIP == "" {
				s.PodIP = hostIP.String()
			}
		}
	}

	return *s
}

// convertStatusToAPIStatus creates an api PodStatus for the given pod from
// the given internal pod status.  It is purely transformative and does not
// alter the kubelet state at all.
func (kl *Kubelet) convertStatusToAPIStatus(pod *v1.Pod, podStatus *kubecontainer.PodStatus) *v1.PodStatus {
	var apiPodStatus v1.PodStatus
	apiPodStatus.PodIP = podStatus.IP
	// set status for Pods created on versions of kube older than 1.6
	apiPodStatus.QOSClass = v1qos.GetPodQOS(pod)


	// TODO statusManager

	//oldPodStatus, found := kl.statusManager.GetPodStatus(pod.UID)
	//if !found {
	//	oldPodStatus = pod.Status
	//}

	oldPodStatus := pod.Status

	apiPodStatus.ContainerStatuses = kl.convertToAPIContainerStatuses(
		pod, podStatus,
		oldPodStatus.ContainerStatuses,
		pod.Spec.Containers,
		len(pod.Spec.InitContainers) > 0,
		false,
	)
	apiPodStatus.InitContainerStatuses = kl.convertToAPIContainerStatuses(
		pod, podStatus,
		oldPodStatus.InitContainerStatuses,
		pod.Spec.InitContainers,
		len(pod.Spec.InitContainers) > 0,
		true,
	)

	// Preserves conditions not controlled by kubelet
	for _, c := range pod.Status.Conditions {
		if !kubetypes.PodConditionByKubelet(c.Type) {
			apiPodStatus.Conditions = append(apiPodStatus.Conditions, c)
		}
	}
	return &apiPodStatus
}


func (kl *Kubelet) getPullSecretsForPod(pod *v1.Pod) []v1.Secret {
	pullSecrets := []v1.Secret{}

	for _, secretRef := range pod.Spec.ImagePullSecrets {
		secret, err := kl.secretManager.GetSecret(pod.Namespace, secretRef.Name)
		if err != nil {
			klog.Warningf("Unable to retrieve pull secret %s/%s for %s/%s due to %v.  The image pull may not succeed.", pod.Namespace, secretRef.Name, pod.Namespace, pod.Name, err)
			continue
		}

		pullSecrets = append(pullSecrets, *secret)
	}

	return pullSecrets

}

// convertToAPIContainerStatuses converts the given internal container
// statuses into API container statuses.
func (kl *Kubelet) convertToAPIContainerStatuses(pod *v1.Pod, podStatus *kubecontainer.PodStatus, previousStatus []v1.ContainerStatus, containers []v1.Container, hasInitContainers, isInitContainer bool) []v1.ContainerStatus {
	convertContainerStatus := func(cs *kubecontainer.ContainerStatus) *v1.ContainerStatus {
		cid := cs.ID.String()
		status := &v1.ContainerStatus{
			Name:         cs.Name,
			RestartCount: int32(cs.RestartCount),
			Image:        cs.Image,
			ImageID:      cs.ImageID,
			ContainerID:  cid,
		}
		switch cs.State {
		case kubecontainer.ContainerStateRunning:
			status.State.Running = &v1.ContainerStateRunning{StartedAt: metav1.NewTime(cs.StartedAt)}
		case kubecontainer.ContainerStateCreated:
			// Treat containers in the "created" state as if they are exited.
			// The pod workers are supposed start all containers it creates in
			// one sync (syncPod) iteration. There should not be any normal
			// "created" containers when the pod worker generates the status at
			// the beginning of a sync iteration.
			fallthrough
		case kubecontainer.ContainerStateExited:
			status.State.Terminated = &v1.ContainerStateTerminated{
				ExitCode:    int32(cs.ExitCode),
				Reason:      cs.Reason,
				Message:     cs.Message,
				StartedAt:   metav1.NewTime(cs.StartedAt),
				FinishedAt:  metav1.NewTime(cs.FinishedAt),
				ContainerID: cid,
			}
		default:
			status.State.Waiting = &v1.ContainerStateWaiting{}
		}
		return status
	}

	// Fetch old containers statuses from old pod status.
	oldStatuses := make(map[string]v1.ContainerStatus, len(containers))
	for _, status := range previousStatus {
		oldStatuses[status.Name] = status
	}

	// Set all container statuses to default waiting state
	statuses := make(map[string]*v1.ContainerStatus, len(containers))
	defaultWaitingState := v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "ContainerCreating"}}
	if hasInitContainers {
		defaultWaitingState = v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "PodInitializing"}}
	}

	for _, container := range containers {
		status := &v1.ContainerStatus{
			Name:  container.Name,
			Image: container.Image,
			State: defaultWaitingState,
		}
		oldStatus, found := oldStatuses[container.Name]
		if found {
			if oldStatus.State.Terminated != nil {
				// Do not update status on terminated init containers as
				// they be removed at any time.
				status = &oldStatus
			} else {
				// Apply some values from the old statuses as the default values.
				status.RestartCount = oldStatus.RestartCount
				status.LastTerminationState = oldStatus.LastTerminationState
			}
		}
		statuses[container.Name] = status
	}

	// Make the latest container status comes first.
	sort.Sort(sort.Reverse(kubecontainer.SortContainerStatusesByCreationTime(podStatus.ContainerStatuses)))
	// Set container statuses according to the statuses seen in pod status
	containerSeen := map[string]int{}
	for _, cStatus := range podStatus.ContainerStatuses {
		cName := cStatus.Name
		if _, ok := statuses[cName]; !ok {
			// This would also ignore the infra container.
			continue
		}
		if containerSeen[cName] >= 2 {
			continue
		}
		status := convertContainerStatus(cStatus)
		if containerSeen[cName] == 0 {
			statuses[cName] = status
		} else {
			statuses[cName].LastTerminationState = status.State
		}
		containerSeen[cName] = containerSeen[cName] + 1
	}

	// Handle the containers failed to be started, which should be in Waiting state.
	for _, container := range containers {
		if isInitContainer {
			// If the init container is terminated with exit code 0, it won't be restarted.
			// TODO(random-liu): Handle this in a cleaner way.
			s := podStatus.FindContainerStatusByName(container.Name)
			if s != nil && s.State == kubecontainer.ContainerStateExited && s.ExitCode == 0 {
				continue
			}
		}
		// If a container should be restarted in next syncpod, it is *Waiting*.
		if !kubecontainer.ShouldContainerBeRestarted(&container, pod, podStatus) {
			continue
		}
		status := statuses[container.Name]
		//reason, ok := kl.reasonCache.Get(pod.UID, container.Name)
		//if !ok {
		//	// In fact, we could also apply Waiting state here, but it is less informative,
		//	// and the container will be restarted soon, so we prefer the original state here.
		//	// Note that with the current implementation of ShouldContainerBeRestarted the original state here
		//	// could be:
		//	//   * Waiting: There is no associated historical container and start failure reason record.
		//	//   * Terminated: The container is terminated.
		//	continue
		//}
		if status.State.Terminated != nil {
			status.LastTerminationState = status.State
		}
		//status.State = v1.ContainerState{
		//	Waiting: &v1.ContainerStateWaiting{
		//		Reason:  reason.Err.Error(),
		//		Message: reason.Message,
		//	},
		//}
		statuses[container.Name] = status
	}

	var containerStatuses []v1.ContainerStatus
	for _, status := range statuses {
		containerStatuses = append(containerStatuses, *status)
	}

	// Sort the container statuses since clients of this interface expect the list
	// of containers in a pod has a deterministic order.
	if isInitContainer {
		kubetypes.SortInitContainerStatuses(pod, containerStatuses)
	} else {
		sort.Sort(kubetypes.SortedContainerStatuses(containerStatuses))
	}
	return containerStatuses
}

// getPhase returns the phase of a pod given its container info.
func getPhase(spec *v1.PodSpec, info []v1.ContainerStatus) v1.PodPhase {
	pendingInitialization := 0
	failedInitialization := 0
	for _, container := range spec.InitContainers {
		containerStatus, ok := podutil.GetContainerStatus(info, container.Name)
		if !ok {
			pendingInitialization++
			continue
		}

		switch {
		case containerStatus.State.Running != nil:
			pendingInitialization++
		case containerStatus.State.Terminated != nil:
			if containerStatus.State.Terminated.ExitCode != 0 {
				failedInitialization++
			}
		case containerStatus.State.Waiting != nil:
			if containerStatus.LastTerminationState.Terminated != nil {
				if containerStatus.LastTerminationState.Terminated.ExitCode != 0 {
					failedInitialization++
				}
			} else {
				pendingInitialization++
			}
		default:
			pendingInitialization++
		}
	}

	unknown := 0
	running := 0
	waiting := 0
	stopped := 0
	succeeded := 0
	for _, container := range spec.Containers {
		containerStatus, ok := podutil.GetContainerStatus(info, container.Name)
		if !ok {
			unknown++
			continue
		}

		switch {
		case containerStatus.State.Running != nil:
			running++
		case containerStatus.State.Terminated != nil:
			stopped++
			if containerStatus.State.Terminated.ExitCode == 0 {
				succeeded++
			}
		case containerStatus.State.Waiting != nil:
			if containerStatus.LastTerminationState.Terminated != nil {
				stopped++
			} else {
				waiting++
			}
		default:
			unknown++
		}
	}

	pretty_containerStatus, _ := json.MarshalIndent(info, "", "\t")
	klog.Infof("===>getPhase pretty_containerStatus: %v", string(pretty_containerStatus))

	klog.Infof("===>getPhase pendingInitialization:%v, waiting: %v, running: %v, unknown: %v, stopped: %v", pendingInitialization, waiting, running, unknown, stopped)

	if failedInitialization > 0 && spec.RestartPolicy == v1.RestartPolicyNever {
		return v1.PodFailed
	}

	switch {
	case pendingInitialization > 0:
		fallthrough
	case waiting > 0:
		klog.V(5).Infof("pod waiting > 0, pending")
		// One or more containers has not been started
		return v1.PodPending
	case running > 0 && unknown == 0:
		// All containers have been started, and at least
		// one container is running
		return v1.PodRunning
	case running == 0 && stopped > 0 && unknown == 0:
		// All containers are terminated
		if spec.RestartPolicy == v1.RestartPolicyAlways {
			// All containers are in the process of restarting
			return v1.PodRunning
		}
		if stopped == succeeded {
			// RestartPolicy is not Always, and all
			// containers are terminated in success
			return v1.PodSucceeded
		}
		if spec.RestartPolicy == v1.RestartPolicyNever {
			// RestartPolicy is Never, and all containers are
			// terminated with at least one in failure
			return v1.PodFailed
		}
		// RestartPolicy is OnFailure, and at least one in failure
		// and in the process of restarting
		return v1.PodRunning
	default:
		klog.V(5).Infof("pod default case, pending")
		return v1.PodPending
	}
}

