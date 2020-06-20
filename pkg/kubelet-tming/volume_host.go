package kubelet_tming

import (
	"k8s.io/kubernetes/pkg/kubelet/secret"
	"k8s.io/kubernetes/pkg/kubelet/configmap"
	"k8s.io/kubernetes/pkg/kubelet/token"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/client-go/informers"
	storagelisters "k8s.io/client-go/listers/storage/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/kubelet-tming/mountpod"
	"fmt"
)

func NewInitializedVolumePluginMgr(
		kubelet *Kubelet,
		secretManager secret.Manager,
		configMapManager configmap.Manager,
		tokenManager 	 *token.Manager,
		plugins 	[]volume.VolumePlugin,
		prober 		volume.DynamicPluginProber) (*volume.VolumePluginMgr, error) {
	// Initialize csiDriverLister before calling InitPlugins
	var informerFactory informers.SharedInformerFactory
	var csiDriverLister storagelisters.CSIDriverLister
	var csiDriversSynced cache.InformerSynced
	const resyncPeriod = 0

	// TODO
	//if utilfeature.DefaultFeatureGate.Enabled(features.CSIDriverRegistry) {
	//	// Don't initialize if kubeClient is nil
	//	if kubelet.kubeClient != nil {
	//		informerFactory = informers.NewSharedInformerFactory(kubelet.kubeClient, resyncPeriod)
	//		csiDriverInformer := informerFactory.Storage().V1beta1().CSIDrivers()
	//		csiDriverLister = csiDriverInformer.Lister()
	//		csiDriversSynced = csiDriverInformer.Informer().HasSynced
	//
	//	} else {
	//		klog.Warning("kubeClient is nil. Skip initialization of CSIDriverLister")
	//	}
	//}

	mountPodManager, err := mountpod.NewManager(kubelet.getRootDir(), kubelet.podManager)
	if err != nil {
		return nil, err
	}
	kvh := &kubeletVolumeHost{
		kubelet: 		kubelet,
		volumePluginMgr:	volume.VolumePluginMgr{},
		secretManager: 		secretManager,
		configMapManager: 	configMapManager,
		tokenManager: 		tokenManager,
		mountPodManager: 	mountPodManager,
		informerFactory: 	informerFactory,
		csiDriverLister: 	csiDriverLister,
		csiDriversSynced: 	csiDriversSynced,
	}
	if err := kvh.volumePluginMgr.InitPlugins(plugins, prober, kvh); err != nil {
		return nil, fmt.Errorf(
			"Could not initialize volume plugins for KubeletVolumePluginMgr: %v", err)
	}
	return &kvh.volumePluginMgr, nil
}

type kubeletVolumeHost struct {
	kubelet          *Kubelet
	volumePluginMgr  volume.VolumePluginMgr
	secretManager    secret.Manager
	tokenManager     *token.Manager
	configMapManager configmap.Manager
	mountPodManager  mountpod.Manager
	informerFactory  informers.SharedInformerFactory
	csiDriverLister  storagelisters.CSIDriverLister
	csiDriversSynced cache.InformerSynced
}