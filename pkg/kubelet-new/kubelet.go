package kubelet_new

import (
	kubetypes "k8s.io/kubernetes/pkg/kubelet-new/types"
	"k8s.io/kubernetes/pkg/kubelet-new/config"
	api "k8s.io/kubernetes/pkg/apis/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/events"
	"k8s.io/klog"
	"k8s.io/apimachinery/pkg/types"
	clientset "k8s.io/client-go/kubernetes"
	nodeutil "k8s.io/kubernetes/pkg/util/node"
	"k8s.io/apimachinery/pkg/util/wait"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	"k8s.io/client-go/tools/record"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	"k8s.io/kubernetes/pkg/kubelet/dockershim"
	dockerremote "k8s.io/kubernetes/pkg/kubelet/dockershim/remote"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"fmt"
	"os"
	"path"
	"time"
	"sync"
	"k8s.io/kubernetes/pkg/kubelet-new/remote"
	internalapi "k8s.io/cri-api/pkg/apis"
	"k8s.io/kubernetes/pkg/kubelet-new/kuberuntime"
	"k8s.io/kubernetes/pkg/kubelet/server/streaming"
	kubepod "k8s.io/kubernetes/pkg/kubelet-new/pod"
	"net/url"
	"net"
	"net/http"
	"k8s.io/kubernetes/pkg/kubelet-new/pleg"
	"k8s.io/kubernetes/pkg/kubelet-new/status"
)

const (
	nodeStatusUpdateRetry = 1

	backOffPeriod = time.Second * 10

	// Capacity of the channel for receiving pod lifecycle events. This number
	// is a bit arbitrary and may be adjusted in the future.
	plegChannelCapacity = 1000

	// Generic PLEG relies on relisting for discovering container events.
	// A longer period means that kubelet will take longer to detect container
	// changes and to update pod status. On the other hand, a shorter period
	// will cause more frequent relisting (e.g., container runtime operations),
	// leading to higher cpu usage.
	// Note that even though we set the period to 1s, the relisting itself can
	// take more than 1s to finish if the container runtime responds slowly
	// and/or when there are many container changes in one cycle.
	plegRelistPeriod = time.Second * 5

	// MaxContainerBackOff is the max backoff period, exported for the e2e test
	MaxContainerBackOff = 300 * time.Second

	// The path in containers' filesystems where the hosts file is mounted.
	etcHostsPath = "/etc/hosts"

	// Period for performing global cleanup tasks.
	housekeepingPeriod = time.Second * 2
)

type Dependencies struct {
	PodConfig               *config.PodConfig
	KubeClient              clientset.Interface
	HeartbeatClient         clientset.Interface
	Recorder                record.EventRecorder
	EventClient             v1core.EventsGetter
	OnHeartbeatFailure      func()
	OSInterface 		kubecontainer.OSInterface
	DockerClientConfig      *dockershim.ClientConfig

}


type Bootstrap interface {
	BirthCry()
	Run(<-chan kubetypes.PodUpdate)
}

type SyncHandler interface {
	HandlePodAdditions(pods []*v1.Pod)
	HandlePodUpdates(pods []*v1.Pod)
	//HandlePodReconcile(pods []*v1.Pod)
	HandlePodSyncs(pods []*v1.Pod)
	HandlePodRemoves(pods []*v1.Pod)
	HandlePodCleanups() error
}


type Kubelet struct {
	kubeClient      clientset.Interface
	heartbeatClient      clientset.Interface
	nodeName        types.NodeName

	// hostname is the hostname the kubelet detected or was given via flag/config
	hostname string
	rootDirectory 	string
	nodeStatusUpdateFrequency time.Duration
	syncNodeStatusMux sync.Mutex

	registerNode bool
	registrationCompleted bool
	registerSchedulable bool

	// handlers called during the tryUpdateNodeStatus cycle
	setNodeStatusFuncs []func(*v1.Node) error

	clock clock.Clock
	// lastStatusReportTime is the time when node status was last reported.
	lastStatusReportTime time.Time
	lastObservedNodeAddressesMux sync.RWMutex
	lastObservedNodeAddresses    []v1.NodeAddress

	// The EventRecorder to use
	recorder record.EventRecorder

	// Reference to this node.
	nodeRef *v1.ObjectReference

	// Container runtime.
	containerRuntime kubecontainer.Runtime

	runtimeService internalapi.RuntimeService

	// The handler serving CRI streaming calls (exec/attach/port-forward).
	criHandler http.Handler


	// dockerLegacyService contains some legacy methods for backward compatibility.
	// It should be set only when docker is using non json-file logging driver.
	dockerLegacyService dockershim.DockerLegacyService

	// Generates pod events.
	pleg pleg.PodLifecycleEventGenerator

	// podManager is a facade that abstracts away the various sources of pods
	// this Kubelet services.
	podManager kubepod.Manager

	// 用于获取docker端的podStatus
	podCache kubecontainer.Cache


	// Syncs pods statuses with apiserver; also used as a cache of statuses.
	statusManager status.Manager

	// trigger deleting containers in a pod
	containerDeletor *podContainerDeletor
}

func getRuntimeAndImageServices(remoteRuntimeEndpoint string, remoteImageEndpoint string, runtimeRequestTimeout metav1.Duration) (internalapi.RuntimeService, internalapi.ImageManagerService, error) {
	rs, err := remote.NewRemoteRuntimeService(remoteRuntimeEndpoint, runtimeRequestTimeout.Duration)
	if err != nil {
		return nil, nil, err
	}
	is, err := remote.NewRemoteImageService(remoteImageEndpoint, runtimeRequestTimeout.Duration)
	if err != nil {
		return nil, nil, err
	}
	return rs, is, err
}

func (kl *Kubelet) BirthCry() {
	klog.Infof("kubelet BirthCry")
}

func (kl *Kubelet) initializeModules() error {

	// Setup filesystem directories.
	if err := kl.setupDataDirs(); err != nil {
		return err
	}

	//kl.imageManager.Start()
	return nil
}

func (kl *Kubelet) setupDataDirs() error {
	kl.rootDirectory = path.Clean(kl.rootDirectory)
	//pluginRegistrationDir := kl.getPluginsRegistrationDir()
	//pluginsDir := kl.getPluginsDir()
	if err := os.MkdirAll(kl.getRootDir(), 0750); err != nil {
		return fmt.Errorf("error creating root directory: %v", err)
	}
	//if err := kl.mounter.MakeRShared(kl.getRootDir()); err != nil {
	//	return fmt.Errorf("error configuring root directory: %v", err)
	//}

	if err := os.MkdirAll(kl.getPodsDir(), 0750); err != nil {
		return fmt.Errorf("error creating pods directory: %v", err)
	}

	if err := os.MkdirAll(kl.getPluginsDir(), 0750); err != nil {
		return fmt.Errorf("error creating plugins directory: %v", err)
	}
	if err := os.MkdirAll(kl.getPluginsRegistrationDir(), 0750); err != nil {
		return fmt.Errorf("error creating plugins registry directory: %v", err)
	}
	//if selinux.SELinuxEnabled() {
	//	err := selinux.SetFileLabel(pluginRegistrationDir, config.KubeletPluginsDirSELinuxLabel)
	//	if err != nil {
	//		klog.Warningf("Unprivileged containerized plugins might not work. Could not set selinux context on %s: %v", pluginRegistrationDir, err)
	//	}
	//	err = selinux.SetFileLabel(pluginsDir, config.KubeletPluginsDirSELinuxLabel)
	//	if err != nil {
	//		klog.Warningf("Unprivileged containerized plugins might not work. Could not set selinux context on %s: %v", pluginsDir, err)
	//	}
	//}

	return nil
}


func (kl *Kubelet) Run(updates <-chan kubetypes.PodUpdate) {
	klog.Infof("kubelet run")

	kl.initializeModules()

	//
	//// Start volume manager
	//go kl.volumeManager.Run(kl.sourcesReady, wait.NeverStop)
	//
	if kl.kubeClient != nil {

		klog.V(5).Infof("syncNodeStatus kl.nodeStatusUpdateFrequency: %v", kl.nodeStatusUpdateFrequency)

		// when k8s delete node, we need to restart kubelet again
		go wait.Until(kl.syncNodeStatus, kl.nodeStatusUpdateFrequency, wait.NeverStop)
	}
	//
	//go wait.Until(kl.updateRuntimeUp, 5*time.Second, wait.NeverStop)
	//
	//kl.statusManager.Start()
	//
	//// Start the pod lifecycle event generator.
	kl.pleg.Start()
	time.Sleep(5 * time.Second)
	kl.syncLoop(updates, kl)
}

func (kl *Kubelet) syncLoop(updates <-chan kubetypes.PodUpdate, handler SyncHandler) {
	klog.Infof("Starting kubelet main sync loop.")
	plegCh := kl.pleg.Watch()

	housekeepingTicker := time.NewTicker(housekeepingPeriod)
	defer housekeepingTicker.Stop()

	for {
		if !kl.syncLoopIteration(updates, kl, plegCh, housekeepingTicker.C) {
			break
		}
		//time.Sleep(time.Minute)
	}

	klog.Infof("syncLoop finishes!")

}

// TODO pleg
func (kl *Kubelet) syncLoopIteration(configCh <-chan kubetypes.PodUpdate,
			handler SyncHandler,
			plegCh chan*pleg.PodLifecycleEvent,
			housekeepingCh <-chan time.Time) bool {
	select {
	case u, open := <- configCh:
		if !open {
			klog.Errorf("Update channel is closed. Exiting the sync loop.")
			return false
		}
		switch u.Op {
		case kubetypes.ADD:
			klog.Infof("SyncLoop (Add, %q): %q", u.Source, format.Pods(u.Pods))
			handler.HandlePodAdditions(u.Pods)
		case kubetypes.UPDATE:
			klog.Infof("SyncLoop (UPDATE, %q): %q", u.Source, format.PodsWithDeletionTimestamps(u.Pods))
			//handler.HandlePodUpdates(u.Pods)

		case kubetypes.RECONCILE:
			klog.Infof("SyncLoop (RECONCILE, %q): %q", u.Source, format.Pods(u.Pods))
			//handler.HandlePodReconcile(u.Pods)

		case kubetypes.DELETE:
			klog.Infof("SyncLoop (DELETE, %q): %q", u.Source, format.Pods(u.Pods))
			handler.HandlePodUpdates(u.Pods)

		case kubetypes.REMOVE:
			klog.Infof("SyncLoop (DELETE, %q): %q", u.Source, format.Pods(u.Pods))
			handler.HandlePodRemoves(u.Pods)
		}
	case e := <-plegCh:
		//if isSyncPodWorthy(e) {
			// PLEG event for a pod; sync it.
			if pod, ok := kl.podManager.GetPodByUID(e.ID); ok {
				klog.Infof("SyncLoop (PLEG): %q, event: %#v", format.Pod(pod), e)
				handler.HandlePodSyncs([]*v1.Pod{pod})
			} else {
				klog.Infof("SyncLoop (PLEG): ignore irrelevant event: %#v", e)
			}
		//}

	// TODO pleg.ContainerDied
		if e.Type == pleg.ContainerDied {
			if containID, ok := e.Data.(string); ok {
				kl.cleanUpContainersInPod(e.ID, containID)
			}
		}

	case <-housekeepingCh:
		if err := handler.HandlePodCleanups(); err != nil {
			klog.Infof("house keeping with err: %v\n", err)
		}
	}
	return true
}

// Delete the eligible dead container instances in a pod.
func (kl *Kubelet) cleanUpContainersInPod(podID types.UID, exitedContainerID string) {
	if podStatus, err := kl.podCache.Get(podID); err == nil {
		removeAll := false
		if syncedPod, ok := kl.podManager.GetPodByUID(podID); ok {
			// generate the api status using the cached runtime status to get up-to-date ContainerStatuses
			apiPodStatus := kl.generateAPIPodStatus(syncedPod, podStatus)
			// When an evicted or deleted pod has already synced, all containers can be removed.
			// TODO eviction.PodIsEvicted(syncedPod.Status)
			removeAll = (syncedPod.DeletionTimestamp != nil && notRunning(apiPodStatus.ContainerStatuses))
		}
		kl.containerDeletor.deleteContainersInPod(exitedContainerID, podStatus, removeAll)
	}
}


func (kl *Kubelet) syncNodeStatus() {
	kl.syncNodeStatusMux.Lock()
	defer kl.syncNodeStatusMux.Unlock()

	if kl.kubeClient == nil {
		return
	}
	if kl.registerNode {
		kl.registerWithAPIServer()
	}

	if err := kl.updateNodeStatus(); err != nil {
		klog.Errorf("Unable to update node status: %v", err)
	}
}

func (kl *Kubelet) updateNodeStatus() error {
	klog.V(5).Infof("updating node status")
	for i := 0; i < nodeStatusUpdateRetry; i++ {
		if err := kl.tryUpdateNodeStatus(i); err != nil {
			//if i > 0 && kl.onRepeatedHeartbeatFailure != nil {
			//	kl.onRepeatedHeartbeatFailure()
			//}
			klog.Errorf("Error updating node status, will retry: %v", err)
		} else {
			return nil
		}
	}
	return fmt.Errorf("update node status exceeds retry count")
}


func makePodSourceConfig(kubeCfg *kubeletconfiginternal.KubeletConfiguration, kubeDeps *Dependencies, nodeName types.NodeName, bootstrapCheckpointPath string) (*config.PodConfig, error) {

	cfg := config.NewPodConfig(config.PodConfigNotificationIncremental)

	var updatechannel chan<- interface{}

	if kubeDeps.KubeClient != nil {
		klog.Infof("Watching apiserver")
		if updatechannel == nil {
			updatechannel = cfg.Channel(kubetypes.ApiserverSource)
		}
		config.NewSourceApiserver(kubeDeps.KubeClient, nodeName, updatechannel)
	}

	return cfg, nil

}

func NewMainKubelet(kubeCfg *kubeletconfiginternal.KubeletConfiguration,
		kubeDeps *Dependencies,
		crOptions *config.ContainerRuntimeOptions,
		containerRuntime string,
		runtimeCgroups string,
		hostnameOverride string,
		nodeIP string,
		providerID string,
		cloudProvider string,
		certDirectory string,
		rootDirectory string,
		registerNode bool,
		registerWithTaints []api.Taint,
		allowedUnsafeSysctls []string,
		remoteRuntimeEndpoint string,
		remoteImageEndpoint string,
		experimentalMounterPath string,
		experimentalKernelMemcgNotification bool,
		experimentalCheckNodeCapabilitiesBeforeMount bool,
		experimentalNodeAllocatableIgnoreEvictionThreshold bool,
		minimumGCAge metav1.Duration,
		maxPerPodContainerCount int32,
		maxContainerCount int32,
		masterServiceNamespace string,
		registerSchedulable bool,
		nonMasqueradeCIDR string,
		keepTerminatedPodVolumes bool,
		nodeLabels map[string]string,
		seccompProfileRoot string,
		bootstrapCheckpointPath string,
		nodeStatusMaxImages int32) (*Kubelet, error) {

	klog.Infof("klet.rootDirectory: %v, CheckpointPath: %v", rootDirectory, crOptions.DockershimRootDirectory)

	if rootDirectory == "" {
		return nil, fmt.Errorf("invalid root directory %q", rootDirectory)
	}

	hostname, err := nodeutil.GetHostname(hostnameOverride)
	if err != nil {
		return nil, err
	}

	nodeName := types.NodeName(hostname)
	if kubeDeps.PodConfig == nil {
		var err error
		kubeDeps.PodConfig, err = makePodSourceConfig(kubeCfg, kubeDeps, nodeName, bootstrapCheckpointPath)
		if err != nil {
			return nil, err
		}
	}

	nodeRef := &v1.ObjectReference{
		Kind:      "Node",
		Name:      string(nodeName),
		UID:       types.UID(nodeName),
		Namespace: "",
	}

	klet := &Kubelet{
		kubeClient: 					kubeDeps.KubeClient,
		heartbeatClient:  				kubeDeps.HeartbeatClient,

		nodeName:   					nodeName,
		hostname:   					string(nodeName),
		rootDirectory:					rootDirectory,
		nodeStatusUpdateFrequency:               	kubeCfg.NodeStatusUpdateFrequency.Duration,
		registerNode: 					registerNode,
		registerSchedulable: 				registerSchedulable,
		clock: 						clock.RealClock{},
		recorder:                                	kubeDeps.Recorder,
		nodeRef: 					nodeRef,
	}

	pluginSettings := dockershim.NetworkPluginSettings{
		HairpinMode:        kubeletconfiginternal.HairpinMode(kubeCfg.HairpinMode),
		NonMasqueradeCIDR:  nonMasqueradeCIDR,
		PluginName:         crOptions.NetworkPluginName,
		PluginConfDir:      crOptions.CNIConfDir,
		PluginBinDirString: crOptions.CNIBinDir,
		MTU:                int(crOptions.NetworkPluginMTU),
	}

	switch containerRuntime {
	case kubetypes.DockerContainerRuntime:
		// Create and start the CRI shim running as a grpc server.
		streamingConfig := getStreamingConfig(kubeCfg, kubeDeps, crOptions)
		ds, err := dockershim.NewDockerService(kubeDeps.DockerClientConfig, crOptions.PodSandboxImage, streamingConfig,
			&pluginSettings, runtimeCgroups, kubeCfg.CgroupDriver, crOptions.DockershimRootDirectory, !crOptions.RedirectContainerStreaming)
		if err != nil {
			return nil, err
		}
		if crOptions.RedirectContainerStreaming {
			klet.criHandler = ds
		}

		// The unix socket for kubelet <-> dockershim communication.
		klog.V(5).Infof("RemoteRuntimeEndpoint: %q, RemoteImageEndpoint: %q",
			remoteRuntimeEndpoint,
			remoteImageEndpoint)
		klog.V(2).Infof("Starting the GRPC server for the docker CRI shim.")
		server := dockerremote.NewDockerServer(remoteRuntimeEndpoint, ds)
		if err := server.Start(); err != nil {
			return nil, err
		}

		// Create dockerLegacyService when the logging driver is not supported.
		supported, err := ds.IsCRISupportedLogDriver()
		if err != nil {
			return nil, err
		}
		if !supported {
			klet.dockerLegacyService = ds
			//legacyLogProvider = ds
		}
	//case kubetypes.RemoteContainerRuntime:
	//	// No-op.
	//	break
	default:
		return nil, fmt.Errorf("unsupported CRI runtime: %q", containerRuntime)
	}

	runtimeService, imageService, err := getRuntimeAndImageServices(remoteRuntimeEndpoint, remoteImageEndpoint, kubeCfg.RuntimeRequestTimeout)
	if err != nil {
		return nil, err
	}
	klet.runtimeService = runtimeService

	runtime, err := kuberuntime.NewKubeGenericRuntimeManager(
		kubeDeps.Recorder,
		kubeDeps.OSInterface,
		runtimeService,
		imageService,
	)
	if err != nil {
		return nil, err
	}
	klet.containerRuntime = runtime

	klet.podCache = kubecontainer.NewCache()

	klet.pleg = pleg.NewGenericPLEG(runtime, plegRelistPeriod, klet.podCache)

	klet.podManager = kubepod.NewBasicPodManager()
	klet.containerDeletor = newPodContainerDeletor(klet.containerRuntime, 100)
	klet.statusManager = status.NewManager(klet.kubeClient, klet)
	// Generating the status funcs should be the last thing we do,
	// since this relies on the rest of the Kubelet having been constructed.
	klet.setNodeStatusFuncs = klet.defaultNodeStatusFuncs()

	return klet, nil
}

func (kl *Kubelet) setLastObservedNodeAddresses(addresses []v1.NodeAddress) {
	kl.lastObservedNodeAddressesMux.Lock()
	defer kl.lastObservedNodeAddressesMux.Unlock()
	kl.lastObservedNodeAddresses = addresses
}
func (kl *Kubelet) getLastObservedNodeAddresses() []v1.NodeAddress {
	kl.lastObservedNodeAddressesMux.RLock()
	defer kl.lastObservedNodeAddressesMux.RUnlock()
	return kl.lastObservedNodeAddresses
}

func (kl *Kubelet) HandlePodRemoves(pods []*v1.Pod) {
	for _, pod := range pods {
		// TODO podManager
		kl.podManager.DeletePod(pod)

		if err := kl.deletePod(pod); err != nil {
			klog.Infof("delete pod %v with err: %v", format.Pod(pod), err)
		}
		// TODO probemanager
	}
}


func (kl *Kubelet) HandlePodAdditions(pods []*v1.Pod) {
	start := kl.clock.Now()
	for _, pod := range pods {
		// TODO podManager
		kl.podManager.AddPod(pod)
		go kl.dispatchWork(pod, kubetypes.SyncPodCreate, nil, start)
	}
}

func (kl *Kubelet) HandlePodSyncs(pods []*v1.Pod) {
	start := kl.clock.Now()
	for _, pod := range pods {
		kl.dispatchWork(pod, kubetypes.SyncPodSync, nil, start)
	}
}

func (kl *Kubelet) HandlePodUpdates(pods []*v1.Pod) {
	start := kl.clock.Now()
	for _, pod := range pods {
		kl.podManager.UpdatePod(pod)
		kl.dispatchWork(pod, kubetypes.SyncPodUpdate, nil, start)
	}
}


func (kl *Kubelet) dispatchWork(pod *v1.Pod, syncType kubetypes.SyncPodType, mirrorPod *v1.Pod, start time.Time) {
	for {
		podStatus, _ := kl.podCache.Get(pod.UID)

		//fmt.Printf("=====>podUid: %v dispatchWork got podStatus: %v\n", pod.UID, podStatus)
		//if podStatus == nil {
		//	fmt.Printf("=====>podUid: %v dispatchWork got podStatus nil\n", pod.UID)
		//}

		opt := syncPodOptions{
			mirrorPod:      mirrorPod,
			pod:            pod,
			podStatus:      podStatus,
			killPodOptions: nil,
			updateType:     syncType,
		}

		//fmt.Printf("=====>podUid: %v dispatchWork to syncPod podStatus: %v\n", pod.UID, podStatus)
		err := kl.syncPod(opt)
		if err == nil {
			return
		}
		time.Sleep(time.Minute)
	}
}

func (kl *Kubelet) syncPod(o syncPodOptions) error {
	pod := o.pod
	podStatus := o.podStatus

	apiPodStatus := kl.generateAPIPodStatus(pod, podStatus)

	//fmt.Printf("status manager before setting podStatus: %v\n", podStatus)
	kl.statusManager.SetPodStatus(pod, apiPodStatus)
	//fmt.Printf("status manager after status manager and syncpod podStatus: %v\n", podStatus)

	if pod.DeletionTimestamp != nil {

		klog.Infof("kubelet is going to delete pod(%v/%v)\n", pod.Namespace, pod.Name)

		var syncErr error
		if err := kl.killPod(pod, nil, podStatus, nil); err != nil {
			kl.recorder.Eventf(pod, v1.EventTypeWarning, events.FailedToKillPod, "error killing pod: %v", err)
			syncErr = fmt.Errorf("error killing pod: %v", err)
			utilruntime.HandleError(syncErr)
		}
		return syncErr
	}

	// Make data directories for the pod
	if err := kl.makePodDataDirs(pod); err != nil {
		kl.recorder.Eventf(pod, v1.EventTypeWarning, events.FailedToMakePodDataDirectories, "error making pod data directories: %v", err)
		klog.Errorf("Unable to make pod data directories for pod %q: %v", format.Pod(pod), err)
		return err
	}
	result := kl.containerRuntime.SyncPod(pod, podStatus)
	if err := result.Error(); err != nil {
		// Do not return error if the only failures were pods in backoff
		for _, r := range result.SyncResults {
			// TODO  r.Error != images.ErrImagePullBackOff
			if r.Error != kubecontainer.ErrCrashLoopBackOff {
				// Do not record an event here, as we keep all event logging for sync pod failures
				// local to container runtime so we get better errors
				return err
			}
		}

		return nil
	}
	return nil
}


// Gets the streaming server configuration to use with in-process CRI shims.
func getStreamingConfig(kubeCfg *kubeletconfiginternal.KubeletConfiguration, kubeDeps *Dependencies, crOptions *config.ContainerRuntimeOptions) *streaming.Config {
	config := &streaming.Config{
		StreamIdleTimeout:               kubeCfg.StreamingConnectionIdleTimeout.Duration,
		StreamCreationTimeout:           streaming.DefaultConfig.StreamCreationTimeout,
		SupportedRemoteCommandProtocols: streaming.DefaultConfig.SupportedRemoteCommandProtocols,
		SupportedPortForwardProtocols:   streaming.DefaultConfig.SupportedPortForwardProtocols,
	}
	if !crOptions.RedirectContainerStreaming {
		config.Addr = net.JoinHostPort("localhost", "0")
	} else {
		// Use a relative redirect (no scheme or host).
		config.BaseURL = &url.URL{
			Path: "/cri/",
		}
		//if kubeDeps.TLSOptions != nil {
		//	config.TLSConfig = kubeDeps.TLSOptions.Config
		//}
	}
	return config
}

func (kl *Kubelet) deletePod(pod *v1.Pod) error {
	if pod == nil {
		return fmt.Errorf("deletePod does not allow nil pod")
	}
	// TODO source Ready
	// TODO runtimecache
	return nil
}














































