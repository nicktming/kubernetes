package reconciler

import (
	"fmt"
	//"strings"
	"time"

	//"k8s.io/api/core/v1"
	//"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/controller/volume/attachdetach-tming/cache"
	"k8s.io/kubernetes/pkg/controller/volume/attachdetach-tming/statusupdater"
	//kevents "k8s.io/kubernetes/pkg/kubelet/events"
	"k8s.io/kubernetes/pkg/util/goroutinemap/exponentialbackoff"
	//"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util/operationexecutor"
)

type Reconciler interface {
	Run(stopCh <-chan struct{})
}

func NewReconciler(
loopPeriod time.Duration,
maxWaitForUnmountDuration time.Duration,
syncDuration time.Duration,
disableReconciliationSync bool,
desiredStateOfWorld cache.DesiredStateOfWorld,
actualStateOfWorld cache.ActualStateOfWorld,
attacherDetacher operationexecutor.OperationExecutor,
nodeStatusUpdater statusupdater.NodeStatusUpdater,
recorder record.EventRecorder) Reconciler {
	return &reconciler{
		loopPeriod:                loopPeriod,
		maxWaitForUnmountDuration: maxWaitForUnmountDuration,
		syncDuration:              syncDuration,
		disableReconciliationSync: disableReconciliationSync,
		desiredStateOfWorld:       desiredStateOfWorld,
		actualStateOfWorld:        actualStateOfWorld,
		attacherDetacher:          attacherDetacher,
		nodeStatusUpdater:         nodeStatusUpdater,
		timeOfLastSync:            time.Now(),
		recorder:                  recorder,
	}
}

type reconciler struct {
	loopPeriod                time.Duration
	maxWaitForUnmountDuration time.Duration
	syncDuration              time.Duration
	desiredStateOfWorld       cache.DesiredStateOfWorld
	actualStateOfWorld        cache.ActualStateOfWorld
	attacherDetacher          operationexecutor.OperationExecutor
	nodeStatusUpdater         statusupdater.NodeStatusUpdater
	timeOfLastSync            time.Time
	disableReconciliationSync bool
	recorder                  record.EventRecorder
}

func (rc *reconciler) Run(stopCh <-chan struct{}) {
	wait.Until(rc.reconciliationLoopFunc(), rc.loopPeriod, stopCh)
}

// reconciliationLoopFunc this can be disabled via cli option disableReconciliation.
// It periodically checks whether the attached volumes from actual state
// are still attached to the node and update the status if they are not.
func (rc *reconciler) reconciliationLoopFunc() func() {
	return func() {

		rc.reconcile()

		if rc.disableReconciliationSync {
			klog.V(5).Info("Skipping reconciling attached volumes still attached since it is disabled via the command line.")
		} else if rc.syncDuration < time.Second {
			klog.V(5).Info("Skipping reconciling attached volumes still attached since it is set to less than one second via the command line.")
		} else if time.Since(rc.timeOfLastSync) > rc.syncDuration {
			klog.V(5).Info("Starting reconciling attached volumes still attached")
			//rc.sync()
			// TODO sync()
		}
	}
}


func (rc *reconciler) reconcile() {
	// TODO Ensure volumes that should be detached are detached

	// Ensure volumes that should be detached are detached.
	for _, attachedVolume := range rc.actualStateOfWorld.GetAttachedVolumes() {
		if !rc.desiredStateOfWorld.VolumeExists(
			attachedVolume.VolumeName, attachedVolume.NodeName) {
			// Don't even try to start an operation if there is already one running
			// This check must be done before we do any other checks, as otherwise the other checks
			// may pass while at the same time the volume leaves the pending state, resulting in
			// double detach attempts
			if rc.attacherDetacher.IsOperationPending(attachedVolume.VolumeName, "") {
				klog.V(10).Infof("Operation for volume %q is already running. Can't start detach for %q", attachedVolume.VolumeName, attachedVolume.NodeName)
				continue
			}

			// Set the detach request time
			elapsedTime, err := rc.actualStateOfWorld.SetDetachRequestTime(attachedVolume.VolumeName, attachedVolume.NodeName)
			if err != nil {
				klog.Errorf("Cannot trigger detach because it fails to set detach request time with error %v", err)
				continue
			}
			// Check whether timeout has reached the maximum waiting time
			timeout := elapsedTime > rc.maxWaitForUnmountDuration
			// Check whether volume is still mounted. Skip detach if it is still mounted unless timeout
			if attachedVolume.MountedByNode && !timeout {
				klog.Infof(attachedVolume.GenerateMsgDetailed("Cannot detach volume because it is still mounted", ""))
				continue
			}

			// Before triggering volume detach, mark volume as detached and update the node status
			// If it fails to update node status, skip detach volume
			err = rc.actualStateOfWorld.RemoveVolumeFromReportAsAttached(attachedVolume.VolumeName, attachedVolume.NodeName)
			if err != nil {
				klog.V(5).Infof("RemoveVolumeFromReportAsAttached failed while removing volume %q from node %q with: %v",
					attachedVolume.VolumeName,
					attachedVolume.NodeName,
					err)
			}

			// Update Node Status to indicate volume is no longer safe to mount.
			err = rc.nodeStatusUpdater.UpdateNodeStatuses()
			if err != nil {
				// Skip detaching this volume if unable to update node status
				klog.Errorf(attachedVolume.GenerateErrorDetailed("UpdateNodeStatuses failed while attempting to report volume as attached", err).Error())
				continue
			}

			// Trigger detach volume which requires verifing safe to detach step
			// If timeout is true, skip verifySafeToDetach check
			klog.Infof(attachedVolume.GenerateMsgDetailed("Starting attacherDetacher.DetachVolume", ""))
			verifySafeToDetach := !timeout
			err = rc.attacherDetacher.DetachVolume(attachedVolume.AttachedVolume, verifySafeToDetach, rc.actualStateOfWorld)
			if err == nil {
				if !timeout {
					klog.Infof(attachedVolume.GenerateMsgDetailed("attacherDetacher.DetachVolume started", ""))
				} else {
					//metrics.RecordForcedDetachMetric()
					klog.Warningf(attachedVolume.GenerateMsgDetailed("attacherDetacher.DetachVolume started", fmt.Sprintf("This volume is not safe to detach, but maxWaitForUnmountDuration %v expired, force detaching", rc.maxWaitForUnmountDuration)))
				}
			}
			if err != nil && !exponentialbackoff.IsExponentialBackoff(err) {
				// Ignore exponentialbackoff.IsExponentialBackoff errors, they are expected.
				// Log all other errors.
				klog.Errorf(attachedVolume.GenerateErrorDetailed("attacherDetacher.DetachVolume failed to start", err).Error())
			}
		}
	}

	rc.attachDesiredVolumes()

	// Update Node Status
	err := rc.nodeStatusUpdater.UpdateNodeStatuses()
	if err != nil {
		klog.Warningf("UpdateNodeStatuses failed with: %v", err)
	}
}

func (rc *reconciler) attachDesiredVolumes() {
	// Ensure volumes that should be attached are attached
	for _, volumeToAttach := range rc.desiredStateOfWorld.GetVolumesToAttach() {
		if rc.actualStateOfWorld.IsVolumeAttachedToNode(volumeToAttach.VolumeName, volumeToAttach.NodeName) {
			//klog.Infof(volumeToAttach.GenerateMsgDetailed("Volume attached--touching", ""))
			// TODO reset detach request time
			rc.actualStateOfWorld.ResetDetachRequestTime(volumeToAttach.VolumeName, volumeToAttach.NodeName)
			continue
		}

		// Don't even try to start an operation if there is already one running
		if rc.attacherDetacher.IsOperationPending(volumeToAttach.VolumeName, "") {
			if klog.V(10) {
				klog.Infof("Operation for volume %q is already running. Can't start attach for %q", volumeToAttach.VolumeName, volumeToAttach.NodeName)
			}
			continue
		}

		// TODO isMultiAttachForbidden

		// Volume/Node doesn't exist, spawn a goroutine to attach it
		klog.Infof(volumeToAttach.GenerateMsgDetailed("Starting attacherDetacher.AttachVolume", ""))
		err := rc.attacherDetacher.AttachVolume(volumeToAttach.VolumeToAttach, rc.actualStateOfWorld)
		if err == nil {
			klog.Infof(volumeToAttach.GenerateMsgDetailed("attacherDetacher.AttachVolume started", ""))
		}
		if err != nil && !exponentialbackoff.IsExponentialBackoff(err) {
			// Ignore exponentialbackoff.IsExponentialBackoff errors, they are expected.
			// Log all other errors.
			klog.Errorf(volumeToAttach.GenerateErrorDetailed("attacherDetacher.AttachVolume failed to start", err).Error())
		}
	}
}
































































