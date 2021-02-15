package kuberuntime

import (
	internalapi "k8s.io/cri-api/pkg/apis"
	"k8s.io/apimachinery/pkg/types"
	"time"
	"k8s.io/klog"
	"k8s.io/apimachinery/pkg/util/sets"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sort"
)

// Newest first.
type sandboxByCreated []sandboxGCInfo

func (a sandboxByCreated) Len() int           { return len(a) }
func (a sandboxByCreated) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a sandboxByCreated) Less(i, j int) bool { return a[i].createTime.After(a[j].createTime) }

// containerGC is the manager of garbage collection.
type containerGC struct {
	client           internalapi.RuntimeService
	manager          *kubeGenericRuntimeManager
	podStateProvider podStateProvider
}

// NewContainerGC creates a new containerGC.
func newContainerGC(client internalapi.RuntimeService, podStateProvider podStateProvider, manager *kubeGenericRuntimeManager) *containerGC {
	return &containerGC{
		client:           client,
		manager:          manager,
		podStateProvider: podStateProvider,
	}
}

// sandboxGCInfo is the internal information kept for sandboxes being considered for GC.
type sandboxGCInfo struct {
	// The ID of the sandbox.
	id string
	// Creation time for the sandbox.
	createTime time.Time
	// If true, the sandbox is ready or still has containers.
	active bool
}

type sandboxesByPodUID map[types.UID][]sandboxGCInfo

// removeOldestNSandboxes removes the oldest inactive toRemove sandboxes and
// returns the resulting slice.
func (cgc *containerGC) removeOldestNSandboxes(sandboxes []sandboxGCInfo, toRemove int) {
	// Remove from oldest to newest (last to first).
	numToKeep := len(sandboxes) - toRemove
	for i := len(sandboxes) - 1; i >= numToKeep; i-- {
		if !sandboxes[i].active {
			cgc.removeSandbox(sandboxes[i].id)
		}
	}
}

func (cgc *containerGC) removeSandbox(sandboxID string) {
	klog.Infof("======>Removing sandbox %q", sandboxID)

	if err := cgc.client.StopPodSandbox(sandboxID); err != nil {
		klog.Errorf("Failed to stop sandbox %q before removing: %v", sandboxID, err)
		return
	}
	if err := cgc.client.RemovePodSandbox(sandboxID); err != nil {
		klog.Errorf("Failed to remove sandbox %q: %v", sandboxID, err)
	}
}


// evictSandboxes remove all evictable sandboxes. An evictable sandbox must
// meet the following requirements:
//   1. not in ready state
//   2. contains no containers.
//   3. belong to a non-existent (i.e., already removed) pod, or is not the
//      most recently created sandbox for the pod.
func (cgc *containerGC) evictSandboxes(evictTerminatedPods bool) error {
	containers, err := cgc.manager.getKubeletContainers(true)
	if err != nil {
		return err
	}

	// collect all the PodSandboxId of container
	sandboxIDs := sets.NewString()
	for _, container := range containers {
		sandboxIDs.Insert(container.PodSandboxId)
	}

	sandboxes, err := cgc.manager.getKubeletSandboxes(true)
	if err != nil {
		return err
	}

	sandboxesByPod := make(sandboxesByPodUID)
	for _, sandbox := range sandboxes {
		podUID := types.UID(sandbox.Metadata.Uid)
		sandboxInfo := sandboxGCInfo{
			id:         sandbox.Id,
			createTime: time.Unix(0, sandbox.CreatedAt),
		}

		// Set ready sandboxes to be active.
		if sandbox.State == runtimeapi.PodSandboxState_SANDBOX_READY {
			sandboxInfo.active = true
		}

		// Set sandboxes that still have containers to be active.
		if sandboxIDs.Has(sandbox.Id) {
			sandboxInfo.active = true
		}

		sandboxesByPod[podUID] = append(sandboxesByPod[podUID], sandboxInfo)
	}
	// Sort the sandboxes by age.
	for uid := range sandboxesByPod {
		sort.Sort(sandboxByCreated(sandboxesByPod[uid]))
	}

	for podUID, sandboxes := range sandboxesByPod {
		// TODO || (cgc.podStateProvider.IsPodTerminated(podUID) && evictTerminatedPods)
		if cgc.podStateProvider.IsPodDeleted(podUID) {
			// Remove all evictable sandboxes if the pod has been removed.
			// Note that the latest dead sandbox is also removed if there is
			// already an active one.
			cgc.removeOldestNSandboxes(sandboxes, len(sandboxes))
		} else {
			// Keep latest one if the pod still exists.
			cgc.removeOldestNSandboxes(sandboxes, len(sandboxes)-1)
		}
	}
	return nil
}

// TODO gc policy allSourcesReady bool
func (cgc *containerGC) GarbageCollect(evictTerminatedPods bool) error {
	errors := []error{}

	// Remove sandboxes with zero containers
	if err := cgc.evictSandboxes(evictTerminatedPods); err != nil {
		errors = append(errors, err)
	}

	return utilerrors.NewAggregate(errors)
}









































































































