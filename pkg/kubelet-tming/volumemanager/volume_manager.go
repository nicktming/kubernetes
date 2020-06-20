package volumemanager

import (
	"time"
	clientset "k8s.io/client-go/kubernetes"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet-tming/pod"
	"k8s.io/kubernetes/pkg/kubelet-tming/status"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/kubelet-tming/container"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/kubelet-tming/config"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet-tming/volumemanager/cache"
	"k8s.io/kubernetes/pkg/kubelet-tming/volumemanager/populator"
	"k8s.io/kubernetes/pkg/volume/util/volumepathhandler"
	"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
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

	// WaitForAttachAndMount processes the volumes referenced in the specified
	// pod and blocks until they are all attached and mounted (reflected in
	// actual state of the world).
	// An error is returned if all volumes are not attached and mounted within
	// the duration defined in podAttachAndMountTimeout.

	// WaitForAttachAndMount(pod *v1.Pod) error

	// GetMountedVolumesForPod returns a VolumeMap containing the volumes
	// referenced by the specified pod that are successfully attached and
	// mounted. The key in the map is the OuterVolumeSpecName (i.e.
	// pod.Spec.Volumes[x].Name). It returns an empty VolumeMap if pod has no
	// volumes.

	// GetMountedVolumesForPod(podName types.UniquePodName) container.VolumeMap

	// GetExtraSupplementalGroupsForPod returns a list of the extra
	// supplemental groups for the Pod. These extra supplemental groups come
	// from annotations on persistent volumes that the pod depends on.

	// GetExtraSupplementalGroupsForPod(pod *v1.Pod) []int64

	// GetVolumesInUse returns a list of all volumes that implement the volume.Attacher
	// interface and are currently in use according to the actual and desired
	// state of the world caches. A volume is considered "in use" as soon as it
	// is added to the desired state of world, indicating it *should* be
	// attached to this node and remains "in use" until it is removed from both
	// the desired state of the world and the actual state of the world, or it
	// has been unmounted (as indicated in actual state of world).

	// GetVolumesInUse() []v1.UniqueVolumeName

	// ReconcilerStatesHasBeenSynced returns true only after the actual states in reconciler
	// has been synced at least once after kubelet starts so that it is safe to update mounted
	// volume list retrieved from actual state.

	// ReconcilerStatesHasBeenSynced() bool

	// VolumeIsAttached returns true if the given volume is attached to this
	// node.

	// VolumeIsAttached(volumeName v1.UniqueVolumeName) bool

	// Marks the specified volume as having successfully been reported as "in
	// use" in the nodes's volume status.

	// MarkVolumesAsReportedInUse(volumesReportedAsInUse []v1.UniqueVolumeName)
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
	recorder record.EventRecorder,
	checkNodeCapabilitiesBeforeMount bool,
	keepTerminatedPodVolumes bool) VolumeManager {

	vm := &volumeManager{
		kubeClient:          kubeClient,
		volumePluginMgr:     volumePluginMgr,
		desiredStateOfWorld: cache.NewDesiredStateOfWorld(volumePluginMgr),
		actualStateOfWorld:  cache.NewActualStateOfWorld(nodeName, volumePluginMgr),
		operationExecutor: operationexecutor.NewOperationExecutor(operationexecutor.NewOperationGenerator(
			kubeClient,
			volumePluginMgr,
			recorder,
			checkNodeCapabilitiesBeforeMount,
			volumepathhandler.NewBlockVolumePathHandler())),
	}

	vm.desiredStateOfWorldPopulator = populator.NewDesiredStateOfWorldPopulator(
		kubeClient,
		desiredStateOfWorldPopulatorLoopSleepPeriod,
		desiredStateOfWorldPopulatorGetPodStatusRetryDuration,
		podManager,
		podStatusProvider,
		vm.desiredStateOfWorld,
		vm.actualStateOfWorld,
		kubeContainerRuntime,
		keepTerminatedPodVolumes)
	//vm.reconciler = reconciler.NewReconciler(
	//	kubeClient,
	//	controllerAttachDetachEnabled,
	//	reconcilerLoopSleepPeriod,
	//	waitForAttachTimeout,
	//	nodeName,
	//	vm.desiredStateOfWorld,
	//	vm.actualStateOfWorld,
	//	vm.desiredStateOfWorldPopulator.HasAddedPods,
	//	vm.operationExecutor,
	//	mounter,
	//	volumePluginMgr,
	//	kubeletPodsDir)

	return vm
}

type volumeManager struct {
	// kubeClient is the kube API client used by DesiredStateOfWorldPopulator to
	// communicate with the API server to fetch PV and PVC objects
	kubeClient clientset.Interface

	// volumePluginMgr is the volume plugin manager used to access volume
	// plugins. It must be pre-initialized.
	volumePluginMgr *volume.VolumePluginMgr

	// desiredStateOfWorld is a data structure containing the desired state of
	// the world according to the volume manager: i.e. what volumes should be
	// attached and which pods are referencing the volumes).
	// The data structure is populated by the desired state of the world
	// populator using the kubelet pod manager.

	desiredStateOfWorld cache.DesiredStateOfWorld

	// actualStateOfWorld is a data structure containing the actual state of
	// the world according to the manager: i.e. which volumes are attached to
	// this node and what pods the volumes are mounted to.
	// The data structure is populated upon successful completion of attach,
	// detach, mount, and unmount actions triggered by the reconciler.

	actualStateOfWorld cache.ActualStateOfWorld

	// operationExecutor is used to start asynchronous attach, detach, mount,
	// and unmount operations.

	operationExecutor operationexecutor.OperationExecutor

	// reconciler runs an asynchronous periodic loop to reconcile the
	// desiredStateOfWorld with the actualStateOfWorld by triggering attach,
	// detach, mount, and unmount operations using the operationExecutor.

	//reconciler reconciler.Reconciler

	// desiredStateOfWorldPopulator runs an asynchronous periodic loop to
	// populate the desiredStateOfWorld using the kubelet PodManager.

	desiredStateOfWorldPopulator populator.DesiredStateOfWorldPopulator

}


func (vm *volumeManager) Run(sourcesReady config.SourcesReady, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()

	// TODO

	go vm.desiredStateOfWorldPopulator.Run(sourcesReady, stopCh)
	//klog.V(2).Infof("The desired_state_of_world populator starts")
	//
	//klog.Infof("Starting Kubelet Volume Manager")
	//go vm.reconciler.Run(stopCh)
	//
	//metrics.Register(vm.actualStateOfWorld, vm.desiredStateOfWorld, vm.volumePluginMgr)

	if vm.kubeClient != nil {
		// start informer for CSIDriver
		vm.volumePluginMgr.Run(stopCh)
	}

	<-stopCh
	klog.Infof("Shutting down Kubelet Volume Manager")
}

