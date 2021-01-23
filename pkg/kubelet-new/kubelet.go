package kubelet_new

import (
	kubetypes "k8s.io/kubernetes/pkg/kubelet-new/types"
	"k8s.io/kubernetes/pkg/kubelet-new/config"
	api "k8s.io/kubernetes/pkg/apis/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeletconfiginternal "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/klog"
)

type Dependencies struct {
	PodConfig               *config.PodConfig
}


type Bootstrap interface {
	BirthCry()
	Run(<-chan kubetypes.PodUpdate)
}

type Kubelet struct {

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
	klet := &Kubelet{

	}
	return klet, nil
}