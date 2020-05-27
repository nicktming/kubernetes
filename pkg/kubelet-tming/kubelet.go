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
)

const (
	nodeStatusUpdateRetry = 5
)

type Dependencies struct {
	//Options []Option
	//
	//// Injected Dependencies
	//Auth                    server.AuthInterface
	//CAdvisorInterface       cadvisor.Interface
	Cloud                   cloudprovider.Interface
	//ContainerManager        cm.ContainerManager
	//DockerClientConfig      *dockershim.ClientConfig
	//EventClient             v1core.EventsGetter
	HeartbeatClient         clientset.Interface
	OnHeartbeatFailure      func()
	KubeClient              clientset.Interface
	//Mounter                 mount.Interface
	//OOMAdjuster             *oom.OOMAdjuster
	//OSInterface             kubecontainer.OSInterface
	PodConfig               *config.PodConfig
	//Recorder                record.EventRecorder
	//Subpather               subpath.Interface
	//VolumePlugins           []volume.VolumePlugin
	//DynamicPluginProber     volume.DynamicPluginProber
	//TLSOptions              *server.TLSOptions
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
}

func (kl *Kubelet) BirthCry() {
	klog.Infof("kubelet BirthCry")
}

func (kl *Kubelet) Run(<-chan kubetypes.PodUpdate) {
	//klog.Infof("kubelet run")

	if kl.kubeClient != nil {
		go wait.Until(kl.syncNodeStatus, kl.nodeStatusUpdateFrequency, wait.NeverStop)
	}

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
	klog.Infof("updating node status")
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

	nodeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if kubeDeps.KubeClient != nil {
		fieldSelector := fields.Set{api.ObjectNameField: string(nodeName)}.AsSelector()
		nodeLW := cache.NewListWatchFromClient(kubeDeps.KubeClient.CoreV1().RESTClient(), "nodes", metav1.NamespaceAll, fieldSelector)
		r := cache.NewReflector(nodeLW, &v1.Node{}, nodeIndexer, 0)
		go r.Run(wait.NeverStop)
	}
	nodeInfo := &predicates.CachedNodeInfo{NodeLister: corelisters.NewNodeLister(nodeIndexer)}

	return &Kubelet{
		kubeClient: kubeDeps.KubeClient,
		nodeName:   nodeName,
		hostname:   string(nodeName),
		heartbeatClient: kubeDeps.HeartbeatClient,
		clock: 		clock.RealClock{},
		onRepeatedHeartbeatFailure: kubeDeps.OnHeartbeatFailure,
		nodeStatusUpdateFrequency:               kubeCfg.NodeStatusUpdateFrequency.Duration,
		nodeInfo:     nodeInfo,
	}, nil

}
