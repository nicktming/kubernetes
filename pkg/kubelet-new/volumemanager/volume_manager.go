package volumemanager


import (
	//"fmt"
	//"sort"
	//"strconv"
	"time"

	//"k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	//"k8s.io/apimachinery/pkg/util/sets"
	//"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	//"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet-new/config"
	//"k8s.io/kubernetes/pkg/kubelet/container"
	//"k8s.io/kubernetes/pkg/kubelet/pod"
	//"k8s.io/kubernetes/pkg/kubelet/status"
	//"k8s.io/kubernetes/pkg/kubelet/util/format"
	//"k8s.io/kubernetes/pkg/kubelet/volumemanager/cache"
	//"k8s.io/kubernetes/pkg/kubelet/volumemanager/metrics"
	//"k8s.io/kubernetes/pkg/kubelet/volumemanager/populator"
	//"k8s.io/kubernetes/pkg/kubelet/volumemanager/reconciler"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/volume"
	//"k8s.io/kubernetes/pkg/volume/util"
	//"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
	//"k8s.io/kubernetes/pkg/volume/util/types"
	//"k8s.io/kubernetes/pkg/volume/util/volumepathhandler"
	"k8s.io/kubernetes/pkg/kubelet-new/volumemanager/populator"
	"k8s.io/kubernetes/pkg/kubelet-new/pod"
	"k8s.io/kubernetes/pkg/kubelet-new/status"
	"k8s.io/kubernetes/pkg/kubelet-new/container"
	"k8s.io/kubernetes/pkg/kubelet-new/volumemanager/cache"
)


const (
	// reconcilerLoopSleepPeriod is the amount of time the reconciler loop waits
	// between successive executions
	reconcilerLoopSleepPeriod = 100 * time.Millisecond

	// desiredStateOfWorldPopulatorLoopSleepPeriod is the amount of time the
	// DesiredStateOfWorldPopulator loop waits between successive executions
	desiredStateOfWorldPopulatorLoopSleepPeriod = 100 * time.Millisecond

	// desiredStateOfWorldPopulatorGetPodStatusRetryDuration is the amount of
	// time the DesiredStateOfWorldPopulator loop waits between successive pod
	// cleanup calls (to prevent calling containerruntime.GetPodStatus too
	// frequently).
	desiredStateOfWorldPopulatorGetPodStatusRetryDuration = 2 * time.Second

	// podAttachAndMountTimeout is the maximum amount of time the
	// WaitForAttachAndMount call will wait for all volumes in the specified pod
	// to be attached and mounted. Even though cloud operations can take several
	// minutes to complete, we set the timeout to 2 minutes because kubelet
	// will retry in the next sync iteration. This frees the associated
	// goroutine of the pod to process newer updates if needed (e.g., a delete
	// request to the pod).
	// Value is slightly offset from 2 minutes to make timeouts due to this
	// constant recognizable.
	podAttachAndMountTimeout = 2*time.Minute + 3*time.Second

	// podAttachAndMountRetryInterval is the amount of time the GetVolumesForPod
	// call waits before retrying
	podAttachAndMountRetryInterval = 300 * time.Millisecond

	// waitForAttachTimeout is the maximum amount of time a
	// operationexecutor.Mount call will wait for a volume to be attached.
	// Set to 10 minutes because we've seen attach operations take several
	// minutes to complete for some volume plugins in some cases. While this
	// operation is waiting it only blocks other operations on the same device,
	// other devices are not affected.
	waitForAttachTimeout = 10 * time.Minute
)

// VolumeManager runs a set of asynchronous loops that figure out which volumes
// need to be attached/mounted/unmounted/detached based on the pods scheduled on
// this node and makes it so.
type VolumeManager interface {
	// Starts the volume manager and all the asynchronous loops that it controls
	Run(sourcesReady config.SourcesReady, stopCh <-chan struct{})
}

// volumeManager implements the VolumeManager interface
type volumeManager struct {
	// kubeClient is the kube API client used by DesiredStateOfWorldPopulator to
	// communicate with the API server to fetch PV and PVC objects
	kubeClient clientset.Interface


	// volumePluginMgr is the volume plugin manager used to access volume
	// plugins. It must be pre-initialized.
	volumePluginMgr *volume.VolumePluginMgr

	// desiredStateOfWorldPopulator runs an asynchronous periodic loop to
	// populate the desiredStateOfWorld using the kubelet PodManager.
	desiredStateOfWorldPopulator populator.DesiredStateOfWorldPopulator


	// desiredStateOfWorld is a data structure containing the desired state of
	// the world according to the volume manager: i.e. what volumes should be
	// attached and which pods are referencing the volumes).
	// The data structure is populated by the desired state of the world
	// populator using the kubelet pod manager.
	desiredStateOfWorld cache.DesiredStateOfWorld
}
func (vm *volumeManager) Run(sourcesReady config.SourcesReady, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()

	go vm.desiredStateOfWorldPopulator.Run(sourcesReady, stopCh)
	klog.V(2).Infof("The desired_state_of_world populator starts")

	klog.Infof("Starting Kubelet Volume Manager")
	//go vm.reconciler.Run(stopCh)

	//metrics.Register(vm.actualStateOfWorld, vm.desiredStateOfWorld, vm.volumePluginMgr)

	//if vm.kubeClient != nil {
	//	// start informer for CSIDriver
	//	vm.volumePluginMgr.Run(stopCh)
	//}

	<-stopCh
	klog.Infof("Shutting down Kubelet Volume Manager")
}

// NewVolumeManager returns a new concrete instance implementing the
// VolumeManager interface.
//
// kubeClient - kubeClient is the kube API client used by DesiredStateOfWorldPopulator
//   to communicate with the API server to fetch PV and PVC objects
// volumePluginMgr - the volume plugin manager used to access volume plugins.
//   Must be pre-initialized.
func NewVolumeManager(
		controllerAttachDetachEnabled bool,
		nodeName k8stypes.NodeName,
		podManager pod.Manager,
		podStatusProvider status.PodStatusProvider,
		kubeClient clientset.Interface,
		volumePluginMgr *volume.VolumePluginMgr,
		kubeContainerRuntime container.Runtime,
		mounter mount.Interface,
		kubeletPodsDir string,
		//recorder record.EventRecorder,
		checkNodeCapabilitiesBeforeMount bool,
		keepTerminatedPodVolumes bool) VolumeManager {

	vm := &volumeManager{
		kubeClient:          kubeClient,
		volumePluginMgr:     volumePluginMgr,
		desiredStateOfWorld: cache.NewDesiredStateOfWorld(volumePluginMgr),
		//actualStateOfWorld:  cache.NewActualStateOfWorld(nodeName, volumePluginMgr),
		//operationExecutor: operationexecutor.NewOperationExecutor(operationexecutor.NewOperationGenerator(
		//	kubeClient,
		//	volumePluginMgr,
		//	recorder,
		//	checkNodeCapabilitiesBeforeMount,
		//	volumepathhandler.NewBlockVolumePathHandler())),
	}

	vm.desiredStateOfWorldPopulator = populator.NewDesiredStateOfWorldPopulator(
		kubeClient,
		desiredStateOfWorldPopulatorLoopSleepPeriod,
		desiredStateOfWorldPopulatorGetPodStatusRetryDuration,
		podManager,
		podStatusProvider,
		vm.desiredStateOfWorld,
		//vm.actualStateOfWorld,
		kubeContainerRuntime,
		keepTerminatedPodVolumes)

	return vm
}