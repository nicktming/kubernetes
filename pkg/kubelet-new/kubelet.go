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
	"k8s.io/apimachinery/pkg/util/sets"
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
	"k8s.io/kubernetes/pkg/kubelet/util/queue"
	"k8s.io/kubernetes/pkg/kubelet-new/prober"
	"k8s.io/kubernetes/pkg/kubelet-new/cm"
	"k8s.io/kubernetes/pkg/kubelet-new/lifecycle"
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

	// ContainerGCPeriod is the period for performing container garbage collection.
	ContainerGCPeriod = time.Minute
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

	ContainerManager 	cm.ContainerManager
}


type Bootstrap interface {
	BirthCry()
	Run(<-chan kubetypes.PodUpdate)
	StartGarbageCollection()
}

type SyncHandler interface {
	HandlePodAdditions(pods []*v1.Pod)
	HandlePodUpdates(pods []*v1.Pod)
	HandlePodReconcile(pods []*v1.Pod)
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

	workQueue queue.WorkQueue

	// podWorkers handle syncing Pods in response to events.
	podWorkers PodWorkers

	probeManager prober.Manager

	// Policy for handling garbage collection of dead containers.
	containerGC kubecontainer.ContainerGC

	// Optional, defaults to simple Docker implementation
	runner kubecontainer.ContainerCommandRunner

	// Manager of non-Runtime containers.
	containerManager cm.ContainerManager

	// updateRuntimeMux is a lock on updating runtime, because this path is not thread-safe.
	// This lock is used by Kubelet.updateRuntimeUp function and shouldn't be used anywhere else.
	updateRuntimeMux sync.Mutex

	// oneTimeInitializer is used to initialize modules that are dependent on the runtime to be up.
	oneTimeInitializer sync.Once

	// TODO: think about moving this to be centralized in PodWorkers in follow-on.
	// the list of handlers to call during pod admission.
	admitHandlers lifecycle.PodAdmitHandlers
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
	go wait.Until(kl.updateRuntimeUp, 5*time.Second, wait.NeverStop)
	//
	kl.statusManager.Start()
	kl.probeManager.Start()

	//
	//// Start the pod lifecycle event generator.
	kl.pleg.Start()
	time.Sleep(5 * time.Second)
	kl.syncLoop(updates, kl)
}

// updateRuntimeUp calls the container runtime status callback, initializing
// the runtime dependent modules when the container runtime first comes up,
// and returns an error if the status check fails.  If the status check is OK,
// update the container runtime uptime in the kubelet runtimeState.
func (kl *Kubelet) updateRuntimeUp() {
	kl.updateRuntimeMux.Lock()
	defer kl.updateRuntimeMux.Unlock()

	kl.oneTimeInitializer.Do(kl.initializeRuntimeDependentModules)
}

// initializeRuntimeDependentModules will initialize internal modules that require the container runtime to be up.
func (kl *Kubelet) initializeRuntimeDependentModules() {
	// containerManager must start after cAdvisor because it needs filesystem capacity information
	// TODO node source ready
	if err := kl.containerManager.Start(nil, kl.GetActivePods, true, kl.statusManager, kl.runtimeService); err != nil {
		// Fail kubelet and rely on the babysitter to retry starting kubelet.
		klog.Fatalf("Failed to start ContainerManager %v", err)
	}
}

func (kl *Kubelet) syncLoop(updates <-chan kubetypes.PodUpdate, handler SyncHandler) {
	klog.Infof("Starting kubelet main sync loop.")
	plegCh := kl.pleg.Watch()

	syncTicker := time.NewTicker(time.Second)
	defer syncTicker.Stop()

	housekeepingTicker := time.NewTicker(housekeepingPeriod)
	defer housekeepingTicker.Stop()

	for {
		if !kl.syncLoopIteration(updates, kl, plegCh, housekeepingTicker.C, syncTicker.C) {
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
			housekeepingCh <-chan time.Time,
			syncCh <-chan time.Time) bool {
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
			handler.HandlePodReconcile(u.Pods)

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
			klog.Infof("======>e.containerdied: %v\n", e)
			if containID, ok := e.Data.(string); ok {
				kl.cleanUpContainersInPod(e.ID, containID)
			}
		}

	case <-syncCh:
	// Sync pods waiting for sync
		podsToSync := kl.getPodsToSync()
		if len(podsToSync) == 0 {
			break
		}
		klog.V(4).Infof("SyncLoop (SYNC): %d pods; %s", len(podsToSync), format.Pods(podsToSync))
		handler.HandlePodSyncs(podsToSync)

	case <-housekeepingCh:
		// TODO sources ready


	}
	return true
}

// Delete the eligible dead container instances in a pod.
func (kl *Kubelet) cleanUpContainersInPod(podID types.UID, exitedContainerID string) {
	klog.Infof("======>cleanUpContainersInPod podID: %v, exitedContainerID: %v\n", podID, exitedContainerID)
	if podStatus, err := kl.podCache.Get(podID); err == nil {
		removeAll := false
		if syncedPod, ok := kl.podManager.GetPodByUID(podID); ok {
			// generate the api status using the cached runtime status to get up-to-date ContainerStatuses
			apiPodStatus := kl.generateAPIPodStatus(syncedPod, podStatus)
			// When an evicted or deleted pod has already synced, all containers can be removed.
			// TODO eviction.PodIsEvicted(syncedPod.Status)
			removeAll = (syncedPod.DeletionTimestamp != nil && notRunning(apiPodStatus.ContainerStatuses))
		}
		klog.Infof("======>cleanUpContainersInPod podID: %v, exitedContainerID: %v, podStatus: %v, removeAll: %v\n",
			podID, exitedContainerID, podStatus, removeAll)
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
		containerManager:                        	kubeDeps.ContainerManager,
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
		klet,
	)
	if err != nil {
		return nil, err
	}
	klet.containerRuntime = runtime
	klet.runner = runtime

	klet.podCache = kubecontainer.NewCache()

	klet.pleg = pleg.NewGenericPLEG(runtime, plegRelistPeriod, klet.podCache)

	klet.podManager = kubepod.NewBasicPodManager()
	klet.containerDeletor = newPodContainerDeletor(klet.containerRuntime, 0)
	klet.statusManager = status.NewManager(klet.kubeClient, klet, klet.podManager)

	klet.probeManager = prober.NewManager(klet.statusManager, klet.runner, klet.recorder)
	// TODO
	containerGC, err := kubecontainer.NewContainerGC(klet.containerRuntime)
	if err != nil {
		return nil, err
	}
	klet.containerGC = containerGC

	{
		volumeAdmitHandler, _ := lifecycle.NewRuntimeAdmitHandler()
		klet.admitHandlers.AddPodAdmitHandler(volumeAdmitHandler)
	}


	klet.workQueue = queue.NewBasicWorkQueue(klet.clock)
	klet.podWorkers = newPodWorkers(klet.syncPod, kubeDeps.Recorder, klet.workQueue, 5 * time.Second, backOffPeriod, klet.podCache)
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
		kl.probeManager.RemovePod(pod)
	}
}

func (kl *Kubelet) getPodsToSync() []*v1.Pod {
	allPods := kl.podManager.GetPods()
	podUIDs := kl.workQueue.GetWork()
	podUIDSet := sets.NewString()
	for _, podUID := range podUIDs {
		podUIDSet.Insert(string(podUID))
	}
	var podsToSync []*v1.Pod
	for _, pod := range allPods {
		if podUIDSet.Has(string(pod.UID)) {
			// The work of the pod is ready
			podsToSync = append(podsToSync, pod)
			continue
		}
		// TODO PodSyncLoopHandlers
		//for _, podSyncLoopHandler := range kl.PodSyncLoopHandlers {
		//	if podSyncLoopHandler.ShouldSync(pod) {
		//		podsToSync = append(podsToSync, pod)
		//		break
		//	}
		//}
	}
	return podsToSync
}


func (kl *Kubelet) HandlePodAdditions(pods []*v1.Pod) {
	start := kl.clock.Now()
	for _, pod := range pods {
		// TODO podManager
		kl.podManager.AddPod(pod)

		if !kl.podIsTerminated(pod) {
			// Only go through the admission process if the pod is not
			// terminated.

			// We failed pods that we rejected, so activePods include all admitted
			// pods that are alive.
			//activePods := kl.filterOutTerminatedPods(existingPods)

			// Check if we can admit the pod; if not, reject it.
			if ok, reason, message := kl.canAdmitPod(nil, pod); !ok {
				kl.rejectPod(pod, reason, message)
				continue
			}
		}

		kl.dispatchWork(pod, kubetypes.SyncPodCreate, nil, start)
		kl.probeManager.AddPod(pod)
	}
}

// canAdmitPod determines if a pod can be admitted, and gives a reason if it
// cannot. "pod" is new pod, while "pods" are all admitted pods
// The function returns a boolean value indicating whether the pod
// can be admitted, a brief single-word reason and a message explaining why
// the pod cannot be admitted.
func (kl *Kubelet) canAdmitPod(pods []*v1.Pod, pod *v1.Pod) (bool, string, string) {
	// the kubelet will invoke each pod admit handler in sequence
	// if any handler rejects, the pod is rejected.
	// TODO: move out of disk check into a pod admitter
	// TODO: out of resource eviction should have a pod admitter call-out
	attrs := &lifecycle.PodAdmitAttributes{Pod: pod, OtherPods: pods}
	for _, podAdmitHandler := range kl.admitHandlers {
		if result := podAdmitHandler.Admit(attrs); !result.Admit {
			return false, result.Reason, result.Message
		}
	}

	return true, "", ""
}

// rejectPod records an event about the pod with the given reason and message,
// and updates the pod to the failed phase in the status manage.
func (kl *Kubelet) rejectPod(pod *v1.Pod, reason, message string) {
	kl.recorder.Eventf(pod, v1.EventTypeWarning, reason, message)
	kl.statusManager.SetPodStatus(pod, v1.PodStatus{
		Phase:   v1.PodFailed,
		Reason:  reason,
		Message: "Pod " + message})
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

func (kl *Kubelet) HandlePodReconcile(pods []*v1.Pod) {
	start := kl.clock.Now()
	for _, pod := range pods {
		kl.podManager.UpdatePod(pod)
		kl.dispatchWork(pod, kubetypes.SyncPodUpdate, nil, start)
	}
}


func (kl *Kubelet) dispatchWork(pod *v1.Pod, syncType kubetypes.SyncPodType, mirrorPod *v1.Pod, start time.Time) {
	//podStatus, _ := kl.podCache.Get(pod.UID)

	//fmt.Printf("=====>podUid: %v dispatchWork got podStatus: %v\n", pod.UID, podStatus)
	//if podStatus == nil {
	//	fmt.Printf("=====>podUid: %v dispatchWork got podStatus nil\n", pod.UID)
	//}

	// Run the sync in an async worker.
	kl.podWorkers.UpdatePod(&UpdatePodOptions{
		Pod:        pod,
		MirrorPod:  mirrorPod,
		UpdateType: syncType,
	})

	//opt := syncPodOptions{
	//	mirrorPod:      mirrorPod,
	//	pod:            pod,
	//	podStatus:      podStatus,
	//	killPodOptions: nil,
	//	updateType:     syncType,
	//}

	//fmt.Printf("=====>podUid: %v dispatchWork to syncPod podStatus: %v\n", pod.UID, podStatus)
	//err := kl.syncPod(opt)
	//if err == nil {
	//	kl.workQueue.Enqueue(pod.UID, 2 * time.Second)
	//	return
	//} else {
	//	kl.workQueue.Enqueue(pod.UID, 5 * time.Second)
	//}
}

func (kl *Kubelet) syncPod(o syncPodOptions) error {

	pod := o.pod
	podStatus := o.podStatus
	klog.Infof("++++++++++++++++++kubelet syncPod pod(%v/%v)\n", pod.Namespace, pod.Name)
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
	klog.Infof("calling containRuntime syncPod")
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


func (kl *Kubelet) StartGarbageCollection() {
	loggedContainerGCFailure := false
	go wait.Until(func() {
		if err := kl.containerGC.GarbageCollect(); err != nil {
			klog.Errorf("Container garbage collection failed: %v", err)
			kl.recorder.Eventf(kl.nodeRef, v1.EventTypeWarning, events.ContainerGCFailed, err.Error())
			loggedContainerGCFailure = true
		} else {
			var vLevel klog.Level = 4
			if loggedContainerGCFailure {
				vLevel = 1
				loggedContainerGCFailure = false
			}

			klog.V(vLevel).Infof("Container garbage collection succeeded")
		}
	}, ContainerGCPeriod, wait.NeverStop)
}











































