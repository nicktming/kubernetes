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
	"fmt"
)

type Dependencies struct {
	PodConfig               *config.PodConfig
	KubeClient              clientset.Interface
}


type Bootstrap interface {
	BirthCry()
	Run(<-chan kubetypes.PodUpdate)
}

type Kubelet struct {
	kubeClient      clientset.Interface
	nodeName        types.NodeName

	// hostname is the hostname the kubelet detected or was given via flag/config
	hostname string
}

func (kl *Kubelet) BirthCry() {
	klog.Infof("kubelet BirthCry")
}

func (kl *Kubelet) Run(updates <-chan kubetypes.PodUpdate) {
	//klog.Infof("kubelet run")
	//
	//kl.initializeModules()
	//
	//
	//// Start volume manager
	//go kl.volumeManager.Run(kl.sourcesReady, wait.NeverStop)
	//
	//if kl.kubeClient != nil {
	//
	//	klog.V(5).Infof("syncNodeStatus kl.nodeStatusUpdateFrequency: %v", kl.nodeStatusUpdateFrequency)
	//
	//	// when k8s delete node, we need to restart kubelet again
	//	go wait.Until(kl.syncNodeStatus, kl.nodeStatusUpdateFrequency, wait.NeverStop)
	//}
	//
	//go wait.Until(kl.updateRuntimeUp, 5*time.Second, wait.NeverStop)
	//
	//kl.statusManager.Start()
	//
	//// Start the pod lifecycle event generator.
	//kl.pleg.Start()
	//kl.syncLoop(updates, kl)
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

	klet := &Kubelet{
		kubeClient: 					kubeDeps.KubeClient,
		nodeName:   					nodeName,
		hostname:   					string(nodeName),
	}

	return klet, nil
}