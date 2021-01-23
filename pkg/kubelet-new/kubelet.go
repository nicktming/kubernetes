package kubelet_new

import (
	kubetypes "k8s.io/kubernetes/pkg/kubelet-new/types"
	"k8s.io/kubernetes/pkg/kubelet-new/config"
	api "k8s.io/kubernetes/pkg/apis/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"
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
	"fmt"
	"os"
	"path"
	"time"
	"sync"
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

	// MaxContainerBackOff is the max backoff period, exported for the e2e test
	MaxContainerBackOff = 300 * time.Second

	// The path in containers' filesystems where the hosts file is mounted.
	etcHostsPath = "/etc/hosts"
)

type Dependencies struct {
	PodConfig               *config.PodConfig
	KubeClient              clientset.Interface
	HeartbeatClient         clientset.Interface
	Recorder                record.EventRecorder
	EventClient             v1core.EventsGetter
	OnHeartbeatFailure      func()

}


type Bootstrap interface {
	BirthCry()
	Run(<-chan kubetypes.PodUpdate)
}

type SyncHandler interface {
	//HandlePodAdditions(pods []*v1.Pod)
	//HandlePodUpdates(pods []*v1.Pod)
	//HandlePodReconcile(pods []*v1.Pod)
	//HandlePodSyncs(pods []*v1.Pod)
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
	//kl.pleg.Start()
	kl.syncLoop(updates, kl)
}

func (kl *Kubelet) syncLoop(updates <-chan kubetypes.PodUpdate, handler SyncHandler) {
	klog.Infof("Starting kubelet main sync loop.")

	//plegCh := kl.pleg.Watch()

	for {
		if !kl.syncLoopIteration(updates, kl) {
			break
		}
		//time.Sleep(time.Minute)
	}

	klog.Infof("syncLoop finishes!")

}

// TODO pleg
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
			//handler.HandlePodAdditions(u.Pods)
		//case kubetypes.UPDATE:
		//	klog.Infof("SyncLoop (UPDATE, %q): %q", u.Source, format.PodsWithDeletionTimestamps(u.Pods))
		//	handler.HandlePodUpdates(u.Pods)

		case kubetypes.RECONCILE:
			klog.Infof("SyncLoop (RECONCILE, %q): %q", u.Source, format.Pods(u.Pods))
			//handler.HandlePodReconcile(u.Pods)
		}
	//case e := <-plegCh:
	//	if isSyncPodWorthy(e) {
	//		// PLEG event for a pod; sync it.
	//		if pod, ok := kl.podManager.GetPodByUID(e.ID); ok {
	//			klog.Infof("SyncLoop (PLEG): %q, event: %#v", format.Pod(pod), e)
	//			handler.HandlePodSyncs([]*v1.Pod{pod})
	//		} else {
	//			klog.Infof("SyncLoop (PLEG): ignore irrelevant event: %#v", e)
	//		}
	//	}

	// TODO pleg.ContainerDied
	//if e.Type == pleg.ContainerDied {
	//	if containID, ok := e.Data.(string); ok {
	//		kl.cle
	//	}
	//}
	}
	return true
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