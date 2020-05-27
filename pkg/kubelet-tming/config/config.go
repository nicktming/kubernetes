package config

import (
	kubetypes "k8s.io/kubernetes/pkg/kubelet-tming/types"
	"k8s.io/kubernetes/pkg/util/config"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/types"
	"sync"
	"k8s.io/api/core/v1"
	"k8s.io/klog"
	"reflect"
	"k8s.io/kubernetes/pkg/kubelet/util/format"
)

type PodConfigNotificationMode int

const (
	PodConfigNotificationUnknown = iota

	PodConfigNotificationSnapshot

	PodConfigNotificationSnapshotAndUpdates

	PodConfigNotificationIncremental
)

type PodConfig struct {
	pods 		*podStorage
	mux		*config.Mux

	updates 	chan kubetypes.PodUpdate

	sourcesLock 	sync.Mutex
	sources 	sets.String

}

func (c *PodConfig) Updates() <-chan kubetypes.PodUpdate {
	return c.updates
}

func (c *PodConfig) Channel(source string) chan<- interface{} {
	c.sourcesLock.Lock()
	defer c.sourcesLock.Unlock()

	c.sources.Insert(source)
	return c.mux.Channel(source)
}


func NewPodConfig(mode PodConfigNotificationMode) *PodConfig {
	updates := make(chan kubetypes.PodUpdate, 50)
	storage := newPodStorage(updates, mode)

	podConfig := &PodConfig {
		pods: 	storage,
		mux: 	config.NewMux(storage),
		updates: updates,
		sources: sets.String{},
	}
	return podConfig
}

type podStorage struct {
	podLock 	sync.RWMutex

	pods 		map[string]map[types.UID]*v1.Pod
	mode 		PodConfigNotificationMode

	updateLock 	sync.Mutex
	updates 	chan<- kubetypes.PodUpdate

	sourcesSeenLock sync.RWMutex
	sourcesSeen	sets.String
}

func newPodStorage(updates chan<- kubetypes.PodUpdate, mode PodConfigNotificationMode) *podStorage {
	return &podStorage {
		pods:		make(map[string]map[types.UID]*v1.Pod),
		mode: 		mode,
		updates: 	updates,
		sourcesSeen:  	sets.String{},
	}
}

func (s *podStorage) Merge(source string, change interface{}) error {
	s.updateLock.Lock()
	defer s.updateLock.Unlock()

	seenBefore := s.sourcesSeen.Has(source)

	adds, updates, deletes, removes, reconciles, restores := s.merge(source, change)

	firstSet := !seenBefore && s.sourcesSeen.Has(source)

	switch s.mode {
	case PodConfigNotificationIncremental:
		if len(removes.Pods) > 0 {
			s.updates <- *removes
		}
		if len(adds.Pods) > 0 {
			s.updates <- *adds
		}
		if len(updates.Pods) > 0 {
			s.updates <- *updates
		}
		if len(restores.Pods) > 0 {
			s.updates <- *restores
		}

		if firstSet && len(adds.Pods) == 0 && len(updates.Pods) == 0 && len(deletes.Pods) == 0 {
			s.updates <- *adds
		}

		if len(reconciles.Pods) > 0 {
			s.updates <- *reconciles
		}
 	}
	return nil
}


func (s *podStorage) merge(source string, change interface{}) (adds, updates, deletes, removes, reconciles, restores *kubetypes.PodUpdate) {
	s.podLock.Lock()
	defer s.podLock.Unlock()

	addPods       := []*v1.Pod{}
	updatePods    := []*v1.Pod{}
	deletePods    := []*v1.Pod{}
	removePods    := []*v1.Pod{}
	reconcilePods := []*v1.Pod{}
	restorePods   := []*v1.Pod{}

	pods := s.pods[source]
	if pods == nil {
		pods = make(map[types.UID]*v1.Pod)
	}

	updatePodsFunc := func(newPods []*v1.Pod, oldPods, pods map[types.UID]*v1.Pod) {
		filtered := newPods
		for _, ref := range filtered {
			if ref.Annotations == nil {
				ref.Annotations = make(map[string]string)
			}
			ref.Annotations[kubetypes.ConfigSourceAnnotationKey] = source
			if existing, found := oldPods[ref.UID]; found {
				pods[ref.UID] = existing
				needUpdate, needReconcile, needGracefulDelete := checkAndUpdatePod(existing, ref)
				if needUpdate {
					klog.Info("update new pods from source %s : %s", source, existing.Name)
					updatePods = append(updatePods, existing)
				} else if needReconcile {
					klog.Info("Status changes pods from source %s : %s", source, existing.Name)
					reconcilePods = append(reconcilePods, existing)
				} else if needGracefulDelete {
					klog.Info("Graceful deleting pods from source %s : %s", source, existing.Name)
					deletePods = append(deletePods, existing)
				}
				continue
			}
			recordFirstSeenTime(ref)
			pods[ref.UID] = ref
			addPods = append(addPods, ref)
		}
	}

	update := change.(kubetypes.PodUpdate)

	switch update.Op {
	case kubetypes.ADD, kubetypes.UPDATE, kubetypes.DELETE:
		if update.Op == kubetypes.ADD {
			klog.Info("Adding new pods from source %s : %v", source, update.Pods)
		} else if update.Op == kubetypes.DELETE {
			klog.Info("Graceful deleting pods from source %s : %v", source, update.Pods)
		} else {
			klog.Info("Updating pods from source %s : %v", source, update.Pods)
		}
		updatePodsFunc(update.Pods, pods, pods)

	case kubetypes.REMOVE:
		klog.Infof("Removing pods from source %s : %v", source, update.Pods)
		for _, value := range update.Pods {
			if existing, found := pods[value.UID]; found {
				delete(pods, value.UID)
				removePods = append(removePods, existing)
				continue
			}
		}

	case kubetypes.SET:
		klog.Infof("Setting pods for source %s", source)
		s.markSourceSet(source)

		oldPods := pods
		pods = make(map[types.UID]*v1.Pod)
		updatePodsFunc(update.Pods, oldPods, pods)

		for uid, existing := range oldPods {
			if _, found := pods[uid]; !found {
				removePods = append(removePods, existing)
			}
		}

	case kubetypes.RESTORE:
		klog.Infof("Restoring pods for source %s", source)
		restorePods = append(restorePods, update.Pods...)

	default:
		klog.Warningf("Received invalid update type: %v", update)

	}

	s.pods[source] = pods

	adds       = &kubetypes.PodUpdate{Op: kubetypes.ADD, Pods: copyPods(addPods), Source: source}
	updates    = &kubetypes.PodUpdate{Op: kubetypes.UPDATE, Pods: copyPods(updatePods), Source: source}
	deletes    = &kubetypes.PodUpdate{Op: kubetypes.DELETE, Pods: copyPods(deletePods), Source: source}
	removes    = &kubetypes.PodUpdate{Op: kubetypes.REMOVE, Pods: copyPods(removePods), Source: source}
	reconciles = &kubetypes.PodUpdate{Op: kubetypes.RECONCILE, Pods: copyPods(reconcilePods), Source: source}
	restores   = &kubetypes.PodUpdate{Op: kubetypes.RESTORE, Pods: copyPods(restorePods), Source: source}

	return adds, updates, deletes, removes, reconciles, restores
}

func (s *podStorage) markSourceSet(source string) {
	s.sourcesSeenLock.Lock()
	defer s.sourcesSeenLock.Unlock()

	s.sourcesSeen.Insert(source)
}

func (s *podStorage) seenSources(sources ...string) bool {
	s.sourcesSeenLock.RLock()
	defer s.sourcesSeenLock.RUnlock()
	return s.sourcesSeen.HasAll(sources...)
}

func recordFirstSeenTime(pod *v1.Pod) {
	klog.Infof("Receiving a new pod %q", format.Pod(pod))
	pod.Annotations[kubetypes.ConfigFirstSeenAnnotationKey] = kubetypes.NewTimestamp().GetString()
}

func checkAndUpdatePod(existing, ref *v1.Pod) (needUpdate, needReconcile, needGracefulDelete bool) {
	if !podsDifferSemantically(existing, ref) {
		if !reflect.DeepEqual(existing.Status, ref.Status) {
			existing.Status = ref.Status
			needReconcile = true
		}
		return
	}
	ref.Annotations[kubetypes.ConfigFirstSeenAnnotationKey] = existing.Annotations[kubetypes.ConfigFirstSeenAnnotationKey]

	existing.Spec = ref.Spec
	existing.Labels = ref.Labels
	existing.DeletionTimestamp = ref.DeletionTimestamp
	existing.DeletionGracePeriodSeconds = ref.DeletionGracePeriodSeconds
	existing.Status = ref.Status
	updateAnnotations(existing, ref)

	if ref.DeletionTimestamp != nil {
		needGracefulDelete = true
	} else {
		needUpdate = true
	}

	return
}

func updateAnnotations(existing, ref *v1.Pod) {
	annotations := make(map[string]string, len(ref.Annotations)+len(localAnnotations))
	for k, v := range ref.Annotations {
		annotations[k] = v
	}
	for _, k := range localAnnotations {
		if v, ok := existing.Annotations[k]; ok {
			annotations[k] = v
		}
	}
	existing.Annotations = annotations
}

func podsDifferSemantically(existing, ref *v1.Pod) bool {
	if reflect.DeepEqual(existing.Spec, ref.Spec) &&
		reflect.DeepEqual(existing.Labels, ref.Labels) &&
		reflect.DeepEqual(existing.DeletionTimestamp, ref.DeletionTimestamp) &&
		reflect.DeepEqual(existing.DeletionGracePeriodSeconds, ref.DeletionGracePeriodSeconds) &&
		isAnnotationMapEqual(existing.Annotations, ref.Annotations) {
		return false
	}
	return true
}

func isAnnotationMapEqual(existingMap, candidateMap map[string]string) bool {
	if candidateMap == nil {
		candidateMap = make(map[string]string)
	}
	for k, v := range candidateMap {
		if isLocalAnnotationKey(k) {
			continue
		}
		if existingValue, ok := existingMap[k]; ok && existingValue == v {
			continue
		}
		return false
	}
	for k := range existingMap {
		if isLocalAnnotationKey(k) {
			continue
		}
		// stale entry in existing map.
		if _, exists := candidateMap[k]; !exists {
			return false
		}
	}
	return true
}

var localAnnotations = []string{
	kubetypes.ConfigSourceAnnotationKey,
	kubetypes.ConfigMirrorAnnotationKey,
	kubetypes.ConfigFirstSeenAnnotationKey,
}

func isLocalAnnotationKey(key string) bool {
	for _, localKey := range localAnnotations {
		if key == localKey {
			return true
		}
	}
	return false
}

func copyPods(sourcePods []*v1.Pod) []*v1.Pod {
	pods := []*v1.Pod{}
	for _, source := range sourcePods {
		// Use a deep copy here just in case
		pods = append(pods, source.DeepCopy())
	}
	return pods
}
