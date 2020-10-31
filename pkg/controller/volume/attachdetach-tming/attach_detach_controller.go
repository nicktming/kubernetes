package attachdetach

import (
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	kcache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/api/core/v1"
	"k8s.io/klog"
	"k8s.io/apimachinery/pkg/types"

	// Volume plugins
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/apimachinery/pkg/util/runtime"
	"time"
	"fmt"
	volumeutil "k8s.io/kubernetes/pkg/volume/util"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/kubernetes/pkg/controller/volume/attachdetach-tming/util"
	"k8s.io/kubernetes/pkg/controller/volume/attachdetach-tming/cache"
	"k8s.io/kubernetes/pkg/volume/rbd"
	toolcache "k8s.io/client-go/tools/cache"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/kubernetes/pkg/util/mount"
	"net"
	csiclient "k8s.io/csi-api/pkg/client/clientset/versioned"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/kubernetes/pkg/controller/volume/attachdetach-tming/reconciler"
	"k8s.io/kubernetes/pkg/controller/volume/attachdetach-tming/populator"
	"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
	"k8s.io/kubernetes/pkg/controller/volume/attachdetach-tming/statusupdater"
	"k8s.io/kubernetes/pkg/volume/util/volumepathhandler"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

// TimerConfig contains configuration of internal attach/detach timers and
// should be used only to speed up tests. DefaultTimerConfig is the suggested
// timer configuration for production.
type TimerConfig struct {
	// ReconcilerLoopPeriod is the amount of time the reconciler loop waits
	// between successive executions
	ReconcilerLoopPeriod time.Duration

	// ReconcilerMaxWaitForUnmountDuration is the maximum amount of time the
	// attach detach controller will wait for a volume to be safely unmounted
	// from its node. Once this time has expired, the controller will assume the
	// node or kubelet are unresponsive and will detach the volume anyway.
	ReconcilerMaxWaitForUnmountDuration time.Duration

	// DesiredStateOfWorldPopulatorLoopSleepPeriod is the amount of time the
	// DesiredStateOfWorldPopulator loop waits between successive executions
	DesiredStateOfWorldPopulatorLoopSleepPeriod time.Duration

	// DesiredStateOfWorldPopulatorListPodsRetryDuration is the amount of
	// time the DesiredStateOfWorldPopulator loop waits between list pods
	// calls.
	DesiredStateOfWorldPopulatorListPodsRetryDuration time.Duration
}

// DefaultTimerConfig is the default configuration of Attach/Detach controller
// timers.
var DefaultTimerConfig TimerConfig = TimerConfig{
	ReconcilerLoopPeriod:                              100 * time.Millisecond,
	ReconcilerMaxWaitForUnmountDuration:               6 * time.Minute,
	DesiredStateOfWorldPopulatorLoopSleepPeriod:       1 * time.Minute,
	DesiredStateOfWorldPopulatorListPodsRetryDuration: 3 * time.Minute,
}

type AttachDetachController interface {
	// TODO add functions
	Run(stopCh <-chan struct{})
}

type attachDetachController struct {
	kubeClient 	clientset.Interface

	pvcLister 	corelisters.PersistentVolumeClaimLister
	pvcsSynced 	kcache.InformerSynced

	pvLister 	corelisters.PersistentVolumeLister
	pvsSynced 	kcache.InformerSynced

	podLister  corelisters.PodLister
	podsSynced kcache.InformerSynced
	podIndexer kcache.Indexer

	nodeLister  corelisters.NodeLister
	nodesSynced kcache.InformerSynced

	pvcQueue workqueue.RateLimitingInterface

	desiredStateOfWorld cache.DesiredStateOfWorld

	volumePluginMgr volume.VolumePluginMgr

	reconciler reconciler.Reconciler
	actualStateOfWorld cache.ActualStateOfWorld
	attacherDetacher operationexecutor.OperationExecutor
	nodeStatusUpdater statusupdater.NodeStatusUpdater
	desiredStateOfWorldPopulator populator.DesiredStateOfWorldPopulator
}

func ProbeAttachableVolumePlugins() []volume.VolumePlugin {
	allPlugins := []volume.VolumePlugin{}

	allPlugins = append(allPlugins, rbd.ProbeVolumePlugins()...)

	return allPlugins
}

func NewAttachDetachController(
	kubeClient 	clientset.Interface,
	podInformer 	coreinformers.PodInformer,
	nodeInformer 	coreinformers.NodeInformer,
	pvcInformer 	coreinformers.PersistentVolumeClaimInformer,
	pvInformer 	coreinformers.PersistentVolumeInformer,
	plugins 	[]volume.VolumePlugin,
	disableReconciliationSync bool,
	reconcilerSyncDuration time.Duration,
	timerConfig TimerConfig) (AttachDetachController, error) {

	adc := &attachDetachController{
		kubeClient: 		kubeClient,
		pvcLister: 		pvcInformer.Lister(),
		pvcsSynced: 		pvcInformer.Informer().HasSynced,
		pvLister: 		pvInformer.Lister(),
		pvsSynced: 		pvInformer.Informer().HasSynced,

		podLister: 		podInformer.Lister(),
		podsSynced: 		podInformer.Informer().HasSynced,
		podIndexer: 		podInformer.Informer().GetIndexer(),
		nodeLister: 		nodeInformer.Lister(),
		nodesSynced: 		nodeInformer.Informer().HasSynced,

		pvcQueue: 		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "pvcs"),
	}

	// TODO adc.volumePluginMgr.InitPlugins
	if err := adc.volumePluginMgr.InitPlugins(plugins, nil, adc); err != nil {
		return nil, fmt.Errorf("Could not initialize volume plugins for Attach/Detach Controller: %+v", err)
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "attachdetach-controller"})
	blkutil := volumepathhandler.NewBlockVolumePathHandler()

	podInformer.Informer().AddEventHandler(kcache.ResourceEventHandlerFuncs{
		AddFunc: 	adc.podAdd,
		UpdateFunc: 	adc.podUpdate,
		DeleteFunc: 	adc.podDelete,
	})

	nodeInformer.Informer().AddEventHandler(kcache.ResourceEventHandlerFuncs{
		AddFunc: 	adc.nodeAdd,
		DeleteFunc:	adc.nodeDelete,
	})

	adc.desiredStateOfWorld = cache.NewDesiredStateOfWorld(&adc.volumePluginMgr)
	adc.actualStateOfWorld = cache.NewActualStateOfWorld(&adc.volumePluginMgr)
	adc.attacherDetacher =
		operationexecutor.NewOperationExecutor(operationexecutor.NewOperationGenerator(
			kubeClient,
			&adc.volumePluginMgr,
			recorder,
			false, // flag for experimental binary check for volume mount
			blkutil))
	adc.nodeStatusUpdater = statusupdater.NewNodeStatusUpdater(
		kubeClient, nodeInformer.Lister(), adc.actualStateOfWorld)

	// Default these to values in options
	adc.reconciler = reconciler.NewReconciler(
		timerConfig.ReconcilerLoopPeriod,
		timerConfig.ReconcilerMaxWaitForUnmountDuration,
		reconcilerSyncDuration,
		disableReconciliationSync,
		adc.desiredStateOfWorld,
		adc.actualStateOfWorld,
		adc.attacherDetacher,
		adc.nodeStatusUpdater,
		recorder)

	adc.desiredStateOfWorldPopulator = populator.NewDesiredStateOfWorldPopulator(
		timerConfig.DesiredStateOfWorldPopulatorLoopSleepPeriod,
		timerConfig.DesiredStateOfWorldPopulatorListPodsRetryDuration,
		podInformer.Lister(),
		adc.desiredStateOfWorld,
		&adc.volumePluginMgr,
		pvcInformer.Lister(),
		pvInformer.Lister())

	// This custom indexer will index pods by its PVC keys. Then we don't need
	// to iterate all pods every time to find pods which reference given PVC.
	adc.podIndexer.AddIndexers(kcache.Indexers{
		pvcKeyIndex: indexByPVCKey,
	})

	pvcInformer.Informer().AddEventHandler(kcache.ResourceEventHandlerFuncs{
		AddFunc: 	func(obj interface{}) {
				adc.enqueuePVC(obj)
		},
		UpdateFunc: 	func(old, new interface{}) {
				adc.enqueuePVC(new)
		},
	})

	return adc, nil
}


func (adc *attachDetachController) Run(stopCh <-chan struct{}) {
	defer runtime.HandleCrash()
	defer adc.pvcQueue.ShutDown()

	klog.Infof("Starting attach detach controller")
	defer klog.Infof("Shutting down attach detach controller")

	synced := []kcache.InformerSynced{adc.podsSynced, adc.nodesSynced, adc.pvcsSynced, adc.pvsSynced}

	if !toolcache.WaitForCacheSync(stopCh, synced...) {
		return
	}

	//err := adc.populateActualStateOfWorld()
	//if err != nil {
	//	klog.Errorf("Error populating the actual state of world: %v", err)
	//}
	//err = adc.populateDesiredStateOfWorld()
	//if err != nil {
	//	klog.Errorf("Error populating the desired state of world: %v", err)
	//}
	go adc.reconciler.Run(stopCh)
	go adc.desiredStateOfWorldPopulator.Run(stopCh)
	go wait.Until(adc.pvcWorker, time.Second, stopCh)

	<-stopCh
}

// indexByPVCKey returns PVC keys for given pod. Note that the index is only
// used for attaching, so we are only interested in active pods with nodeName
// set.
func indexByPVCKey(obj interface{}) ([]string, error) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return []string{}, nil
	}
	if len(pod.Spec.NodeName) == 0 || volumeutil.IsPodTerminated(pod, pod.Status) {
		return []string{}, nil
	}
	keys := []string{}
	for _, podVolume := range pod.Spec.Volumes {
		if pvcSource := podVolume.VolumeSource.PersistentVolumeClaim; pvcSource != nil {
			keys = append(keys, fmt.Sprintf("%s/%s", pod.Namespace, pvcSource.ClaimName))
		}
	}
	return keys, nil
}


func (adc *attachDetachController) enqueuePVC(obj interface{}) {
	key, err := kcache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(fmt.Errorf("Couldn't get key for object %+v: %v", obj, err))
		return
	}
	adc.pvcQueue.Add(key)
}

func (adc *attachDetachController) pvcWorker() {
	for adc.processNextItem() {
	}
}

func (adc *attachDetachController) processNextItem() bool {
	keyObj, shutdown := adc.pvcQueue.Get()
	if shutdown {
		return false
	}
	defer adc.pvcQueue.Done(keyObj)

	if err := adc.syncPVCByKey(keyObj.(string)); err != nil {
		// Rather than wait for a full resync, re-add the key to the
		// queue to be processed.
		adc.pvcQueue.AddRateLimited(keyObj)
		runtime.HandleError(fmt.Errorf("Failed to sync pvc %q, will retry again: %v", keyObj.(string), err))
		return true
	}

	// Finally, if no error occurs we Forget this item so it does not
	// get queued again until another change happens.
	adc.pvcQueue.Forget(keyObj)
	return true
}

const (
	pvcKeyIndex string = "pvcKey"
)


func (adc *attachDetachController) syncPVCByKey(key string) error {
	klog.V(5).Infof("syncPVCByKey[%s]", key)
	namespace, name, err := kcache.SplitMetaNamespaceKey(key)
	if err != nil {
		klog.V(4).Infof("error getting namespace & name of pvc %q to get pvc from informer: %v", key, err)
		return nil
	}
	pvc, err := adc.pvcLister.PersistentVolumeClaims(namespace).Get(name)
	if apierrors.IsNotFound(err) {
		klog.V(4).Infof("error getting pvc %q from informer: %v", key, err)
		return nil
	}
	if err != nil {
		return err
	}

	if pvc.Status.Phase != v1.ClaimBound || pvc.Spec.VolumeName == "" {
		// Skip unbound PVCs.
		return nil
	}

	objs, err := adc.podIndexer.ByIndex(pvcKeyIndex, key)
	if err != nil {
		return err
	}
	for _, obj := range objs {
		pod, ok := obj.(*v1.Pod)
		if !ok {
			continue
		}

		volumeActionFlag := util.DetermineVolumeAction(
			pod,
			adc.desiredStateOfWorld,
			true)
		klog.Infof("syncPVCByKey process pod %v/%v with action: %v (true represents addvolume, false represents deletevolume)",
			pod.Namespace, pod.Name, volumeActionFlag)
		util.ProcessPodVolumes(pod, volumeActionFlag, /* addVolumes */
			adc.desiredStateOfWorld, &adc.volumePluginMgr, adc.pvcLister, adc.pvLister)
	}
	return nil
}

func (adc *attachDetachController) nodeAdd(obj interface{}) {
	node, ok := obj.(*v1.Node)
	// TODO: investigate if nodeName is empty then if we can return
	// kubernetes/kubernetes/issues/37777
	if node == nil || !ok {
		return
	}
	nodeName := types.NodeName(node.Name)
	adc.nodeUpdate(nil, obj)
	// kubernetes/kubernetes/issues/37586
	// This is to workaround the case when a node add causes to wipe out
	// the attached volumes field. This function ensures that we sync with
	// the actual status.
	adc.actualStateOfWorld.SetNodeStatusUpdateNeeded(nodeName)
}

func (adc *attachDetachController) nodeUpdate(oldObj, newObj interface{}) {
	node, ok := newObj.(*v1.Node)
	// TODO: investigate if nodeName is empty then if we can return
	if node == nil || !ok {
		return
	}

	nodeName := types.NodeName(node.Name)
	adc.addNodeToDswp(node, nodeName)
	adc.processVolumesInUse(nodeName, node.Status.VolumesInUse)
}



// processVolumesInUse processes the list of volumes marked as "in-use"
// according to the specified Node's Status.VolumesInUse and updates the
// corresponding volume in the actual state of the world to indicate that it is
// mounted.
func (adc *attachDetachController) processVolumesInUse(
nodeName types.NodeName, volumesInUse []v1.UniqueVolumeName) {
	klog.V(4).Infof("processVolumesInUse for node %q", nodeName)
	for _, attachedVolume := range adc.actualStateOfWorld.GetAttachedVolumesForNode(nodeName) {
		mounted := false
		for _, volumeInUse := range volumesInUse {
			if attachedVolume.VolumeName == volumeInUse {
				mounted = true
				break
			}
		}
		err := adc.actualStateOfWorld.SetVolumeMountedByNode(attachedVolume.VolumeName, nodeName, mounted)
		if err != nil {
			klog.Warningf(
				"SetVolumeMountedByNode(%q, %q, %v) returned an error: %v",
				attachedVolume.VolumeName, nodeName, mounted, err)
		}
	}
}



func (adc *attachDetachController) nodeDelete(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if node == nil || !ok {
		return
	}

	nodeName := types.NodeName(node.Name)
	if err := adc.desiredStateOfWorld.DeleteNode(nodeName); err != nil {
		// This might happen during drain, but we still want it to appear in our logs
		klog.Infof("error removing node %q from desired-state-of-world: %v", nodeName, err)
	}

	adc.processVolumesInUse(nodeName, node.Status.VolumesInUse)
}


func (adc *attachDetachController) podAdd(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if pod == nil || !ok {
		return
	}

	if pod.Spec.NodeName == "" {
		return
	}
	klog.Infof("podAdd add pod : %v/%v\n", pod.Namespace, pod.Name)
	volumeActionFlag := util.DetermineVolumeAction(
		pod,
		adc.desiredStateOfWorld,
		true)
	klog.Infof("podAdd process pod %v/%v with action: %v (true represents addvolume, false represents deletevolume)",
		pod.Namespace, pod.Name, volumeActionFlag)
	util.ProcessPodVolumes(pod, volumeActionFlag, /* addVolumes */
		adc.desiredStateOfWorld, &adc.volumePluginMgr, adc.pvcLister, adc.pvLister)
}

func (adc *attachDetachController) podUpdate(oldObj, newObj interface{}) {
	pod, ok := newObj.(*v1.Pod)
	if pod == nil || !ok {
		return
	}
	if pod.Spec.NodeName == "" {
		return
	}
	klog.Infof("podUpdate update pod : %v/%v\n", pod.Namespace, pod.Name)
	volumeActionFlag := util.DetermineVolumeAction(
		pod,
		adc.desiredStateOfWorld,
		true)
	klog.Infof("podUpdate process pod %v/%v with action: %v (true represents addvolume, false represents deletevolume)",
		pod.Namespace, pod.Name, volumeActionFlag)
	util.ProcessPodVolumes(pod, volumeActionFlag, /* addVolumes */
		adc.desiredStateOfWorld, &adc.volumePluginMgr, adc.pvcLister, adc.pvLister)
}

func (adc *attachDetachController) podDelete(obj interface{}) {
	pod, ok := obj.(*v1.Pod)
	if pod == nil || !ok {
		return
	}
	//klog.Infof("podDelete delete pod : %v/%v\n", pod.Namespace, pod.Name)
	//volumeActionFlag := util.DetermineVolumeAction(
	//	pod,
	//	adc.desiredStateOfWorld,
	//	false)
	//klog.Infof("podDelete process pod %v/%v with action: %v (true represents addvolume, false represents deletevolume)",
	//	pod.Namespace, pod.Name, volumeActionFlag)
	util.ProcessPodVolumes(pod, false,
		adc.desiredStateOfWorld, &adc.volumePluginMgr, adc.pvcLister, adc.pvLister)
}

// VolumeHost implementation
// This is an unfortunate requirement of the current factoring of volume plugin
// initializing code. It requires kubelet specific methods used by the mounting
// code to be implemented by all initializers even if the initializer does not
// do mounting (like this attach/detach controller).
// Issue kubernetes/kubernetes/issues/14217 to fix this.
func (adc *attachDetachController) GetPluginDir(podUID string) string {
	return ""
}

func (adc *attachDetachController) GetVolumeDevicePluginDir(podUID string) string {
	return ""
}

func (adc *attachDetachController) GetPodsDir() string {
	return ""
}

func (adc *attachDetachController) GetPodVolumeDir(podUID types.UID, pluginName, volumeName string) string {
	return ""
}

func (adc *attachDetachController) GetPodPluginDir(podUID types.UID, pluginName string) string {
	return ""
}

func (adc *attachDetachController) GetPodVolumeDeviceDir(podUID types.UID, pluginName string) string {
	return ""
}

func (adc *attachDetachController) GetKubeClient() clientset.Interface {
	return adc.kubeClient
}

func (adc *attachDetachController) NewWrapperMounter(volName string, spec volume.Spec, pod *v1.Pod, opts volume.VolumeOptions) (volume.Mounter, error) {
	return nil, fmt.Errorf("NewWrapperMounter not supported by Attach/Detach controller's VolumeHost implementation")
}

func (adc *attachDetachController) NewWrapperUnmounter(volName string, spec volume.Spec, podUID types.UID) (volume.Unmounter, error) {
	return nil, fmt.Errorf("NewWrapperUnmounter not supported by Attach/Detach controller's VolumeHost implementation")
}

func (adc *attachDetachController) GetCloudProvider() cloudprovider.Interface {
	//return adc.cloud
	return nil
}

func (adc *attachDetachController) GetMounter(pluginName string) mount.Interface {
	return nil
}

func (adc *attachDetachController) GetHostName() string {
	return ""
}

func (adc *attachDetachController) GetHostIP() (net.IP, error) {
	return nil, fmt.Errorf("GetHostIP() not supported by Attach/Detach controller's VolumeHost implementation")
}

func (adc *attachDetachController) GetNodeAllocatable() (v1.ResourceList, error) {
	return v1.ResourceList{}, nil
}

func (adc *attachDetachController) GetSecretFunc() func(namespace, name string) (*v1.Secret, error) {
	return func(_, _ string) (*v1.Secret, error) {
		return nil, fmt.Errorf("GetSecret unsupported in attachDetachController")
	}
}

func (adc *attachDetachController) GetConfigMapFunc() func(namespace, name string) (*v1.ConfigMap, error) {
	return func(_, _ string) (*v1.ConfigMap, error) {
		return nil, fmt.Errorf("GetConfigMap unsupported in attachDetachController")
	}
}

func (adc *attachDetachController) GetServiceAccountTokenFunc() func(_, _ string, _ *authenticationv1.TokenRequest) (*authenticationv1.TokenRequest, error) {
	return func(_, _ string, _ *authenticationv1.TokenRequest) (*authenticationv1.TokenRequest, error) {
		return nil, fmt.Errorf("GetServiceAccountToken unsupported in attachDetachController")
	}
}

func (adc *attachDetachController) DeleteServiceAccountTokenFunc() func(types.UID) {
	return func(types.UID) {
		klog.Errorf("DeleteServiceAccountToken unsupported in attachDetachController")
	}
}

func (adc *attachDetachController) GetExec(pluginName string) mount.Exec {
	return mount.NewOsExec()
}

func (adc *attachDetachController) addNodeToDswp(node *v1.Node, nodeName types.NodeName) {
	if _, exists := node.Annotations[volumeutil.ControllerManagedAttachAnnotation]; exists {
		keepTerminatedPodVolumes := false

		if t, ok := node.Annotations[volumeutil.KeepTerminatedPodVolumesAnnotation]; ok {
			keepTerminatedPodVolumes = (t == "true")
		}

		// Node specifies annotation indicating it should be managed by attach
		// detach controller. Add it to desired state of world.
		adc.desiredStateOfWorld.AddNode(nodeName, keepTerminatedPodVolumes)
	}
}

func (adc *attachDetachController) GetNodeLabels() (map[string]string, error) {
	return nil, fmt.Errorf("GetNodeLabels() unsupported in Attach/Detach controller")
}

func (adc *attachDetachController) GetNodeName() types.NodeName {
	return ""
}

func (adc *attachDetachController) GetEventRecorder() record.EventRecorder {
	//return adc.recorder
	return nil
}

func (adc *attachDetachController) GetCSIClient() csiclient.Interface {
	//return adc.csiClient
	return nil
}

