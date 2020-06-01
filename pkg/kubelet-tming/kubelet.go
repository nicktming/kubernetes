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
)

const (
	nodeStatusUpdateRetry = 5
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
	//EventClient             v1core.EventsGetter
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

}

func (kl *Kubelet) BirthCry() {
	klog.Infof("kubelet BirthCry")
}

func (kl *Kubelet) initializeModules() error {
	kl.imageManager.Start()
	return nil
}

func (kl *Kubelet) Run(<-chan kubetypes.PodUpdate) {
	//klog.Infof("kubelet run")

	kl.initializeModules()

	if kl.kubeClient != nil {
		// when k8s delete node, we need to restart kubelet again
		go wait.Until(kl.syncNodeStatus, kl.nodeStatusUpdateFrequency, wait.NeverStop)
	}

	go wait.Until(kl.updateRuntimeUp, 5*time.Second, wait.NeverStop)
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
	}

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

	switch containerRuntime {
	case kubetypes.DockerContainerRuntime:

		streamingConfig := getStreamingConfig(kubeCfg, kubeDeps, crOptions)
		ds, err := dockershim.NewDockerService(kubeDeps.DockerClientConfig, crOptions.PodSandboxImage, streamingConfig,
			&pluginSettings, runtimeCgroups, kubeCfg.CgroupDriver, crOptions.DockershimRootDirectory, !crOptions.RedirectContainerStreaming)
		if err != nil {
			return nil, err
		}
		// TODO criHandler

		klog.Infof("RemoteRuntimeEndpoint: %q, RemoteImageEndpoint: %q",
			remoteRuntimeEndpoint,
			remoteImageEndpoint)
		klog.Infof("Starting the GRPC server for the docker CRI shim.")
		server := dockerremote.NewDockerServer(remoteRuntimeEndpoint, ds)
		if err := server.Start(); err != nil {
			return nil, err
		}
		// TODO ds.IsCRISupportedLogDriver()
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

	runtime, err := kuberuntime.NewKubeGenericRuntimeManager(runtimeService, imageService, klet)
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
