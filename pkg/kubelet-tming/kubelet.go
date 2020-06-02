package kubelet_tming

import (
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/kubelet-tming/config"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	clientset "k8s.io/client-go/kubernetes"
	kubetypes "k8s.io/kubernetes/pkg/kubelet-tming/types"
	nodeutil "k8s.io/kubernetes/pkg/util/node"
	cloudprovider "k8s.io/cloud-provider"
	"sync"
	v1 "k8s.io/api/core/v1"
	"time"
	"k8s.io/apimachinery/pkg/util/clock"
	"fmt"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/kubernetes/pkg/scheduler/algorithm/predicates"
	"k8s.io/client-go/tools/cache"
	"k8s.io/apimachinery/pkg/fields"
	corelisters "k8s.io/client-go/listers/core/v1"
	"net"

	"k8s.io/kubernetes/pkg/kubelet/cloudresource"
	"k8s.io/kubernetes/pkg/kubelet-tming/images"

	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	"net/url"

	"k8s.io/kubernetes/pkg/kubelet/server/streaming"
	"k8s.io/kubernetes/pkg/kubelet/server"
	"k8s.io/kubernetes/pkg/kubelet/dockershim"
	dockerremote "k8s.io/kubernetes/pkg/kubelet/dockershim/remote"
	"k8s.io/kubernetes/pkg/kubelet-tming/remote"

	internalapi "k8s.io/cri-api/pkg/apis"
	"k8s.io/kubernetes/pkg/kubelet-tming/kuberuntime"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/kubelet-tming/cadvisor"
	"k8s.io/kubernetes/pkg/kubelet-tming/cm"
	"k8s.io/kubernetes/pkg/util/mount"

	cadvisorapi "github.com/google/cadvisor/info/v1"
	"k8s.io/kubernetes/pkg/kubelet-tming/status"
	"k8s.io/kubernetes/pkg/kubelet/secret"
	"k8s.io/kubernetes/pkg/kubelet/configmap"

	"k8s.io/kubernetes/pkg/kubelet/util/manager"

	kubepod "k8s.io/kubernetes/pkg/kubelet-tming/pod"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
	"k8s.io/kubernetes/pkg/kubelet/util/queue"

	serverstats "k8s.io/kubernetes/pkg/kubelet-tming/server/stats"
	"k8s.io/kubernetes/pkg/kubelet-tming/eviction"

	"k8s.io/kubernetes/pkg/kubelet-tming/stats"

	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"net/http"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
	//"sort"
	"k8s.io/kubernetes/pkg/kubelet-tming/pleg"

	"k8s.io/kubernetes/pkg/kubelet/metrics"
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
	plegRelistPeriod = time.Second * 1
)

type Dependencies struct {
	//Options []Option
	//
	//// Injected Dependencies
	//Auth                    server.AuthInterface
	CAdvisorInterface       cadvisor.Interface
	Cloud                   cloudprovider.Interface
	ContainerManager        cm.ContainerManager
	DockerClientConfig      *dockershim.ClientConfig
	EventClient             v1core.EventsGetter
	HeartbeatClient         clientset.Interface
	OnHeartbeatFailure      func()
	KubeClient              clientset.Interface
	Mounter                 mount.Interface
	//OOMAdjuster             *oom.OOMAdjuster
	//OSInterface             kubecontainer.OSInterface
	PodConfig               *config.PodConfig
	Recorder                record.EventRecorder
	//Subpather               subpath.Interface
	//VolumePlugins           []volume.VolumePlugin
	//DynamicPluginProber     volume.DynamicPluginProber
	TLSOptions              *server.TLSOptions
	//KubeletConfigController *kubeletconfig.Controller


}

type SyncHandler interface {
	HandlePodAdditions(pods []*v1.Pod)
}

type Bootstrap interface {
	BirthCry()
	Run(<-chan kubetypes.PodUpdate)
}

type Kubelet struct {
	kubeClient      clientset.Interface
	heartbeatClient clientset.Interface

	syncNodeStatusMux sync.Mutex

	registerNode bool
	registrationCompleted bool

	nodeName        types.NodeName

	// hostname is the hostname the kubelet detected or was given via flag/config
	hostname string
	// hostnameOverridden indicates the hostname was overridden via flag/config
	hostnameOverridden bool

	registerSchedulable bool

	// handlers called during the tryUpdateNodeStatus cycle
	setNodeStatusFuncs []func(*v1.Node) error

	// lastStatusReportTime is the time when node status was last reported.
	lastStatusReportTime time.Time

	clock clock.Clock

	lastObservedNodeAddressesMux sync.RWMutex
	lastObservedNodeAddresses    []v1.NodeAddress

	// onRepeatedHeartbeatFailure is called when a heartbeat operation fails more than once. optional.
	onRepeatedHeartbeatFailure func()

	nodeStatusUpdateFrequency time.Duration

	// nodeInfo knows how to get information about the node for this kubelet.
	nodeInfo predicates.NodeInfo

	// If non-nil, use this IP address for the node
	nodeIP net.IP

	nodeIPValidator func(net.IP) error

	// Indicates that the node initialization happens in an external cloud controller
	externalCloudProvider bool

	// Cloud provider interface.
	cloud cloudprovider.Interface
	// Handles requests to cloud provider with timeout
	cloudResourceSyncManager cloudresource.SyncManager

	// Needed to observe and respond to situations that could impact node stability
	//evictionManager eviction.Manager

	// Manager for image garbage collection.
	imageManager images.ImageGCManager

	// The name of the container runtime
	containerRuntimeName string

	// Container runtime.
	containerRuntime kubecontainer.Runtime

	runtimeService internalapi.RuntimeService

	nodeStatusMaxImages int32

	containerManager cm.ContainerManager


	// Maximum Number of Pods which can be run by this Kubelet
	maxPods int

	// the number of allowed pods per core
	podsPerCore int

	// Cached MachineInfo returned by cadvisor.
	machineInfo *cadvisorapi.MachineInfo

	// cAdvisor used for container information.
	cadvisor cadvisor.Interface


	// The EventRecorder to use
	recorder record.EventRecorder

	// Reference to this node.
	nodeRef *v1.ObjectReference

	// oneTimeInitializer is used to initialize modules that are dependent on the runtime to be up.
	oneTimeInitializer sync.Once

	// updateRuntimeMux is a lock on updating runtime, because this path is not thread-safe.
	// This lock is used by Kubelet.updateRuntimeUp function and shouldn't be used anywhere else.
	updateRuntimeMux sync.Mutex

	// sourcesReady records the sources seen by the kubelet, it is thread-safe.
	sourcesReady config.SourcesReady

	// Syncs pods statuses with apiserver; also used as a cache of statuses.
	statusManager status.Manager

	// Information about the ports which are opened by daemons on Node running this Kubelet server.
	daemonEndpoints *v1.NodeDaemonEndpoints

	// Policy for handling garbage collection of dead containers.
	containerGC kubecontainer.ContainerGC

	// Secret manager.
	secretManager secret.Manager

	// ConfigMap manager.
	configMapManager configmap.Manager

	// podManager is a facade that abstracts away the various sources of pods
	// this Kubelet services.
	podManager kubepod.Manager


	// A queue used to trigger pod workers.
	workQueue queue.WorkQueue

	// Store kubecontainer.PodStatus for all pods.
	podCache kubecontainer.Cache

	// podWorkers handle syncing Pods in response to events.
	podWorkers PodWorkers

	// Monitor resource usage
	resourceAnalyzer serverstats.ResourceAnalyzer

	// used to measure usage stats on system
	//summaryProvider serverstats.SummaryProvider

	// Needed to observe and respond to situations that could impact node stability
	evictionManager eviction.Manager

	// StatsProvider provides the node and the container stats.
	*stats.StatsProvider

	// The handler serving CRI streaming calls (exec/attach/port-forward).
	criHandler http.Handler

	// resyncInterval is the interval between periodic full reconciliations of
	// pods on this node.
	resyncInterval time.Duration

	pleg pleg.PodLifecycleEventGenerator

	// dockerLegacyService contains some legacy methods for backward compatibility.
	// It should be set only when docker is using non json-file logging driver.
	dockerLegacyService dockershim.DockerLegacyService

}

func (kl *Kubelet) BirthCry() {
	klog.Infof("kubelet BirthCry")
}

func (kl *Kubelet) initializeModules() error {
	kl.imageManager.Start()
	return nil
}

func (kl *Kubelet) Run(updates <-chan kubetypes.PodUpdate) {
	//klog.Infof("kubelet run")

	kl.initializeModules()

	if kl.kubeClient != nil {

		klog.V(5).Infof("syncNodeStatus kl.nodeStatusUpdateFrequency: %v", kl.nodeStatusUpdateFrequency)

		// when k8s delete node, we need to restart kubelet again
		go wait.Until(kl.syncNodeStatus, kl.nodeStatusUpdateFrequency, wait.NeverStop)
	}

	go wait.Until(kl.updateRuntimeUp, 5*time.Second, wait.NeverStop)

	// Start the pod lifecycle event generator.
	kl.pleg.Start()
	kl.syncLoop(updates, kl)
}


func (kl *Kubelet) syncLoop(updates <-chan kubetypes.PodUpdate, handler SyncHandler) {
	klog.Infof("Starting kubelet main sync loop.")

	for {
		if !kl.syncLoopIteration(updates, kl) {
			break
		}
	}

	klog.Infof("syncLoop finishes!")

}

func (kl *Kubelet) syncLoopIteration(configCh <-chan kubetypes.PodUpdate, handler SyncHandler) bool {
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
		}
	}
	return true
}


func (kl *Kubelet) HandlePodAdditions(pods []*v1.Pod) {
	start := kl.clock.Now()
	//sort.Sort(sliceutils.PodsByCreationTime(pods))

	for _, pod := range pods {
		//if kl.dnsConfigurer != nil && kl.dnsConfigurer.ResolverConfig != "" {
		//	kl.dnsConfigurer.CheckLimitsForResolvConf()
		//}

		//existingPods := kl.podManager.GetPods()

		kl.podManager.AddPod(pod)

		//if kubepod.IsMirrorPod(pod) {
		//	kl.handleMirrorPod(pod, start)
		//	continue
		//}

		//if !kl.podIsTerminated(pod) {
		//	// Only go through the admission process if the pod is not
		//	// terminated.
		//
		//	// We failed pods that we rejected, so activePods include all admitted
		//	// pods that are alive.
		//	activePods := kl.filterOutTerminatedPods(existingPods)
		//
		//	// Check if we can admit the pod; if not, reject it.
		//	if ok, reason, message := kl.canAdmitPod(activePods, pod); !ok {
		//		kl.rejectPod(pod, reason, message)
		//		continue
		//	}
		//}

		mirrorPod, _ := kl.podManager.GetMirrorPodByPod(pod)
		kl.dispatchWork(pod, kubetypes.SyncPodCreate, mirrorPod, start)
		//kl.probeManager.AddPod(pod)

	}
}


func (kl *Kubelet) dispatchWork(pod *v1.Pod, syncType kubetypes.SyncPodType, mirrorPod *v1.Pod, start time.Time) {
	//
	//if kl.podIsTerminated(pod) {
	//	if pod.DeletionTimestamp != nil {
	//		//kl.statusManager.TerminatePod(pod)
	//	}
	//	return
	//}

	kl.podWorkers.UpdatePod(&UpdatePodOptions{
		Pod:		pod,
		MirrorPod: 	mirrorPod,
		UpdateType:	syncType,
		OnCompleteFunc: func(err error) {
			klog.Infof("pod OnCompleteFunc err: %v", err)
		},
	})

	// Note the number of containers for new pods.
	//if syncType == kubetypes.SyncPodCreate {
	//	metrics.ContainersPerPodCount.Observe(float64(len(pod.Spec.Containers)))
	//}
}


func (kl *Kubelet) syncPod(o syncPodOptions) error {
	pod := o.pod
	mirrorPod := o.mirrorPod
	podStatus := o.podStatus
	updateType := o.updateType


	// if we want to kill a pod, do it now!
	//if updateType == kubetypes.SyncPodKill {
	//	killPodOptions := o.killPodOptions
	//	if killPodOptions == nil || killPodOptions.PodStatusFunc == nil {
	//		return fmt.Errorf("kill pod options are required if update type is kill")
	//	}
	//	apiPodStatus := killPodOptions.PodStatusFunc(pod, podStatus)
	//	kl.statusManager.SetPodStatus(pod, apiPodStatus)
	//	// we kill the pod with the specified grace period since this is a termination
	//	if err := kl.killPod(pod, nil, podStatus, killPodOptions.PodTerminationGracePeriodSecondsOverride); err != nil {
	//		kl.recorder.Eventf(pod, v1.EventTypeWarning, events.FailedToKillPod, "error killing pod: %v", err)
	//		// there was an error killing the pod, so we return that error directly
	//		utilruntime.HandleError(err)
	//		return err
	//	}
	//	return nil
	//}

	// Latency measurements for the main workflow are relative to the
	// first time the pod was seen by the API server.
	var firstSeenTime time.Time
	if firstSeenTimeStr, ok := pod.Annotations[kubetypes.ConfigFirstSeenAnnotationKey]; ok {
		firstSeenTime = kubetypes.ConvertToTimestamp(firstSeenTimeStr).Get()
	}

	// Record pod worker start latency if being created
	// TODO: make pod workers record their own latencies
	if updateType == kubetypes.SyncPodCreate {
		if !firstSeenTime.IsZero() {
			// This is the first time we are syncing the pod. Record the latency
			// since kubelet first saw the pod if firstSeenTime is set.
			metrics.PodWorkerStartDuration.Observe(metrics.SinceInSeconds(firstSeenTime))
			metrics.DeprecatedPodWorkerStartLatency.Observe(metrics.SinceInMicroseconds(firstSeenTime))
		} else {
			klog.V(3).Infof("First seen time not recorded for pod %q", pod.UID)
		}
	}

	// Generate final API pod status with pod and status manager status
	apiPodStatus := kl.generateAPIPodStatus(pod, podStatus)

	return nil
}


func (kl *Kubelet) GetActivePods() []*v1.Pod {
	//allPods := kl.podManager.GetPods()
	//activePods := kl.filterOutTerminatedPods(allPods)
	//return activePods

	allPods := make([]*v1.Pod, 0)
	return allPods
}

func (kl *Kubelet) initializeRuntimeDependentModules() {
	if err := kl.cadvisor.Start(); err != nil {
		// Fail kubelet and rely on the babysitter to retry starting kubelet.
		// TODO(random-liu): Add backoff logic in the babysitter
		klog.Fatalf("Failed to start cAdvisor %v", err)
	}

	// trigger on-demand stats collection once so that we have capacity information for ephemeral storage.
	// ignore any errors, since if stats collection is not successful, the container manager will fail to start below.
	//kl.StatsProvider.GetCgroupStats("/", true)
	// Start container manager.
	node, err := kl.getNodeAnyWay()
	if err != nil {
		// Fail kubelet and rely on the babysitter to retry starting kubelet.
		klog.Fatalf("Kubelet failed to get node info: %v", err)
	}
	// containerManager must start after cAdvisor because it needs filesystem capacity information
	if err := kl.containerManager.Start(node, kl.GetActivePods, kl.sourcesReady, kl.statusManager, kl.runtimeService); err != nil {
		// Fail kubelet and rely on the babysitter to retry starting kubelet.
		klog.Fatalf("Failed to start ContainerManager %v", err)
	}
	//// eviction manager must start after cadvisor because it needs to know if the container runtime has a dedicated imagefs
	//kl.evictionManager.Start(kl.StatsProvider, kl.GetActivePods, kl.podResourcesAreReclaimed, evictionMonitoringPeriod)
	//
	//// container log manager must start after container runtime is up to retrieve information from container runtime
	//// and inform container to reopen log file after log rotation.
	//kl.containerLogManager.Start()
	//if kl.enablePluginsWatcher {
	//	// Adding Registration Callback function for CSI Driver
	//	kl.pluginManager.AddHandler(pluginwatcherapi.CSIPlugin, plugincache.PluginHandler(csi.PluginHandler))
	//	// Adding Registration Callback function for Device Manager
	//	kl.pluginManager.AddHandler(pluginwatcherapi.DevicePlugin, kl.containerManager.GetPluginRegistrationHandler())
	//	// Start the plugin manager
	//	klog.V(4).Infof("starting plugin manager")
	//	go kl.pluginManager.Run(kl.sourcesReady, wait.NeverStop)
	//}
}


func (kl *Kubelet) updateRuntimeUp() {
	kl.updateRuntimeMux.Lock()
	defer kl.updateRuntimeMux.Unlock()

	//klog.Info("update Runtime Up")
	// TODO a lot

	kl.oneTimeInitializer.Do(kl.initializeRuntimeDependentModules)

}

func (kl *Kubelet) fastStatusUpdateOnce() {
	//for {
	//	time.Sleep(100 * time.Millisecond)
	//	node, err := kl.GetNode()
	//	if err != nil {
	//		klog.Errorf(err.Error())
	//		continue
	//	}
	//}
}


func (kl *Kubelet) GetNode() (*v1.Node, error) {
	if kl.kubeClient == nil {
		return kl.initialNode()
	}
	return kl.nodeInfo.GetNodeInfo(string(kl.nodeName))
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

	containerGCPolicy := kubecontainer.ContainerGCPolicy{
		MinAge: 		minimumGCAge.Duration,
		MaxPerPodContainer: 	int(maxPerPodContainerCount),
		MaxContainers:		int(maxContainerCount),
	}

	nodeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if kubeDeps.KubeClient != nil {
		fieldSelector := fields.Set{api.ObjectNameField: string(nodeName)}.AsSelector()
		nodeLW := cache.NewListWatchFromClient(kubeDeps.KubeClient.CoreV1().RESTClient(), "nodes", metav1.NamespaceAll, fieldSelector)
		r := cache.NewReflector(nodeLW, &v1.Node{}, nodeIndexer, 0)
		go r.Run(wait.NeverStop)
	}
	nodeInfo := &predicates.CachedNodeInfo{NodeLister: corelisters.NewNodeLister(nodeIndexer)}

	parsedNodeIP := net.ParseIP(nodeIP)
	//protocol := utilipt.ProtocolIpv4
	//if parsedNodeIP != nil && parsedNodeIP.To4() == nil {
	//	klog.V(0).Infof("IPv6 node IP (%s), assume IPv6 operation", nodeIP)
	//	protocol = utilipt.ProtocolIpv6
	//}

	daemonEndpoints := &v1.NodeDaemonEndpoints{
		KubeletEndpoint: v1.DaemonEndpoint{Port: kubeCfg.Port},
	}

	nodeRef := &v1.ObjectReference{
		Kind:      "Node",
		Name:      string(nodeName),
		UID:       types.UID(nodeName),
		Namespace: "",
	}

	klet := &Kubelet{
		kubeClient: 					kubeDeps.KubeClient,
		nodeName:   					nodeName,
		hostname:   					string(nodeName),
		heartbeatClient: 				kubeDeps.HeartbeatClient,
		clock: 						clock.RealClock{},
		onRepeatedHeartbeatFailure: 			kubeDeps.OnHeartbeatFailure,
		nodeStatusUpdateFrequency:               	kubeCfg.NodeStatusUpdateFrequency.Duration,
		nodeInfo:     					nodeInfo,
		registerNode: 					registerNode,
		registerSchedulable: 				registerSchedulable,
		nodeIP:						parsedNodeIP,
		nodeIPValidator:                         	validateNodeIP,
		cloud:                                   	kubeDeps.Cloud,
		externalCloudProvider:                   	cloudprovider.IsExternal(cloudProvider),
		containerRuntimeName:				containerRuntime,
		nodeStatusMaxImages:				-1,
		containerManager:                        	kubeDeps.ContainerManager,
		maxPods:                                 	int(kubeCfg.MaxPods),
		podsPerCore:                             	int(kubeCfg.PodsPerCore),
		cadvisor:                                	kubeDeps.CAdvisorInterface,
		recorder:                                	kubeDeps.Recorder,
		nodeRef: 					nodeRef,
		sourcesReady:                            	config.NewSourcesReady(kubeDeps.PodConfig.SeenAllSources),
		daemonEndpoints: 				daemonEndpoints,
		resyncInterval:                          	kubeCfg.SyncFrequency.Duration,
	}



	klet.nodeStatusUpdateFrequency = time.Duration(1 * time.Minute)

	var secretManager secret.Manager
	var configMapManager configmap.Manager
	switch kubeCfg.ConfigMapAndSecretChangeDetectionStrategy {
	case kubeletconfiginternal.WatchChangeDetectionStrategy:
		secretManager = secret.NewWatchingSecretManager(kubeDeps.KubeClient)
		configMapManager = configmap.NewWatchingConfigMapManager(kubeDeps.KubeClient)
	case kubeletconfiginternal.TTLCacheChangeDetectionStrategy:
		secretManager = secret.NewCachingSecretManager(
			kubeDeps.KubeClient, manager.GetObjectTTLFromNodeFunc(klet.GetNode))
		configMapManager = configmap.NewCachingConfigMapManager(
			kubeDeps.KubeClient, manager.GetObjectTTLFromNodeFunc(klet.GetNode))
	case kubeletconfiginternal.GetChangeDetectionStrategy:
		secretManager = secret.NewSimpleSecretManager(kubeDeps.KubeClient)
		configMapManager = configmap.NewSimpleConfigMapManager(kubeDeps.KubeClient)
	default:
		return nil, fmt.Errorf("unknown configmap and secret manager mode: %v", kubeCfg.ConfigMapAndSecretChangeDetectionStrategy)
	}

	klet.secretManager = secretManager
	klet.configMapManager = configMapManager

	containerGC, err := kubecontainer.NewContainerGC(klet.containerRuntime, containerGCPolicy, klet.sourcesReady)
	if err != nil {
		return nil, err
	}
	klet.containerGC = containerGC

	klet.podCache = kubecontainer.NewCache()
	var checkpointManager checkpointmanager.CheckpointManager
	if bootstrapCheckpointPath != "" {
		checkpointManager, err = checkpointmanager.NewCheckpointManager(bootstrapCheckpointPath)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize checkpoint manager: %+v", err)
		}
	}

	klet.podManager = kubepod.NewBasicPodManager(kubepod.NewBasicMirrorClient(klet.kubeClient), secretManager, configMapManager, checkpointManager)

	if remoteRuntimeEndpoint != "" {
		if remoteImageEndpoint == "" {
			remoteImageEndpoint = remoteRuntimeEndpoint
		}
	}

	pluginSettings := dockershim.NetworkPluginSettings{
		HairpinMode:        kubeletconfiginternal.HairpinMode(kubeCfg.HairpinMode),
		NonMasqueradeCIDR:  nonMasqueradeCIDR,
		PluginName:         crOptions.NetworkPluginName,
		PluginConfDir:      crOptions.CNIConfDir,
		PluginBinDirString: crOptions.CNIBinDir,
		MTU:                int(crOptions.NetworkPluginMTU),
	}

	klet.resourceAnalyzer = serverstats.NewResourceAnalyzer(klet, kubeCfg.VolumeStatsAggPeriod.Duration)


	// if left at nil, that means it is unneeded
	var legacyLogProvider kuberuntime.LegacyLogProvider = nil

	switch containerRuntime {
	case kubetypes.DockerContainerRuntime:

		streamingConfig := getStreamingConfig(kubeCfg, kubeDeps, crOptions)
		ds, err := dockershim.NewDockerService(kubeDeps.DockerClientConfig, crOptions.PodSandboxImage, streamingConfig,
			&pluginSettings, runtimeCgroups, kubeCfg.CgroupDriver, crOptions.DockershimRootDirectory, !crOptions.RedirectContainerStreaming)
		if err != nil {
			return nil, err
		}
		// TODO criHandler

		if crOptions.RedirectContainerStreaming {
			klet.criHandler = ds
		}

		klog.Infof("RemoteRuntimeEndpoint: %q, RemoteImageEndpoint: %q",
			remoteRuntimeEndpoint,
			remoteImageEndpoint)
		klog.Infof("Starting the GRPC server for the docker CRI shim.")
		server := dockerremote.NewDockerServer(remoteRuntimeEndpoint, ds)
		if err := server.Start(); err != nil {
			return nil, err
		}
		// TODO ds.IsCRISupportedLogDriver()

		// Create dockerLegacyService when the logging driver is not supported.
		supported, err := ds.IsCRISupportedLogDriver()
		if err != nil {
			return nil, err
		}
		if !supported {
			klet.dockerLegacyService = ds
			//legacyLogProvider = ds
		}


	default:
		return nil, fmt.Errorf("unsupported CRI runtime: %q", containerRuntime)
	}

	machineInfo, err := klet.cadvisor.MachineInfo()
	if err != nil {
		return nil, err
	}
	klet.machineInfo = machineInfo

	runtimeService, imageService, err := getRuntimeAndImageServices(remoteRuntimeEndpoint, remoteImageEndpoint, kubeCfg.RuntimeRequestTimeout)
	if err != nil {
		return nil, err
	}
	klet.runtimeService = runtimeService

	// TODO RuntimeClass

	//runtime, err := kuberuntime.NewKubeGenericRuntimeManager(runtimeService, imageService, klet, legacyLogProvider)

	runtime, err := kuberuntime.NewKubeGenericRuntimeManager(runtimeService, imageService, klet, legacyLogProvider)

	if err != nil {
		return nil, err
	}

	klet.containerRuntime = runtime

	imageGCPolicy := images.ImageGCPolicy{
		MinAge:               kubeCfg.ImageMinimumGCAge.Duration,
		HighThresholdPercent: int(kubeCfg.ImageGCHighThresholdPercent),
		LowThresholdPercent:  int(kubeCfg.ImageGCLowThresholdPercent),
	}

	imageManager, err := images.NewImageGCManager(klet.containerRuntime, kubeDeps.Recorder, nodeRef, imageGCPolicy, crOptions.PodSandboxImage)

	if err != nil {
		return nil, fmt.Errorf("failed to initialize image manager: %v", err)
	}

	klet.imageManager = imageManager

	klet.workQueue = queue.NewBasicWorkQueue(klet.clock)

	evictionManager := eviction.NewManager(klet.resourceAnalyzer)
	klet.evictionManager = evictionManager

	klet.pleg = pleg.NewGenericPLEG(klet.containerRuntime, plegChannelCapacity, plegRelistPeriod, klet.podCache, clock.RealClock{})

	klet.podWorkers = newPodWorkers(klet.syncPod, kubeDeps.Recorder, klet.workQueue, klet.resyncInterval, backOffPeriod, klet.podCache)

	klet.setNodeStatusFuncs = klet.defaultNodeStatusFuncs()

	return klet, nil

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
		if kubeDeps.TLSOptions != nil {
			config.TLSConfig = kubeDeps.TLSOptions.Config
		}
	}
	return config
}
