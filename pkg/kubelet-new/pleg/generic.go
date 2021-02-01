package pleg

import (
	"k8s.io/apimachinery/pkg/util/wait"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	"time"
	"k8s.io/klog"
	//"encoding/json"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"fmt"
	"sync"
)

type GenericPLEG struct {
	// The period for relisting.
	relistPeriod 	time.Duration

	runtime 	kubecontainer.Runtime

	podRecords 	PodRecords

	eventChannel 	chan*PodLifecycleEvent

	cache 		kubecontainer.Cache

	reListLock 	sync.RWMutex
}

type PodRecord struct {
	old *kubecontainer.Pod
	cur *kubecontainer.Pod
}

type PodRecords map[types.UID]*PodRecord

func (p PodRecords) setCurrent(pods []*kubecontainer.Pod) {
	for _, pod := range pods {
		if _, ok := p[pod.ID]; !ok {
			p[pod.ID] = &PodRecord{}
		} else {
			p[pod.ID].old = p[pod.ID].cur
			p[pod.ID].cur = pod
		}

	}
}

func (p PodRecords) getOld(pid types.UID) *kubecontainer.Pod {
	if _, ok := p[pid]; !ok {
		return nil
	}
	return p[pid].old
}

func (p PodRecords) getCurrent(pid types.UID) *kubecontainer.Pod {
	if _, ok := p[pid]; !ok {
		return nil
	}
	return p[pid].cur
}

func NewGenericPLEG(runtime kubecontainer.Runtime, relistPeriod time.Duration, cache kubecontainer.Cache) PodLifecycleEventGenerator {
	return &GenericPLEG{
		relistPeriod: 	relistPeriod,
		runtime: 	runtime,
		podRecords: 	make(PodRecords),
		eventChannel: 	make(chan *PodLifecycleEvent, 10),
		cache: 		cache,
	}
}


func (g *GenericPLEG) Start() {
	go wait.Until(g.relist, g.relistPeriod, wait.NeverStop)
}

func getContainersFromPod(pods ...*kubecontainer.Pod) []kubecontainer.ContainerID {
	cidSet := sets.NewString()
	var containers []kubecontainer.ContainerID

	for _, pod := range pods {
		if pod == nil {
			continue
		}
		for _, c := range pod.Containers {
			if cidSet.Has(c.ID.ID) {
				continue
			}
			containers = append(containers, c.ID)
		}
		for _, c := range pod.Sandboxes {
			if cidSet.Has(c.ID.ID) {
				continue
			}
			containers = append(containers, c.ID)
		}
	}
	return containers
}

func getContainerState(pod *kubecontainer.Pod, cid kubecontainer.ContainerID) kubecontainer.ContainerState {
	if pod == nil {
		return kubecontainer.ContainerStateUnknown
	}
	for _, c := range pod.Containers {
		if c.ID.ID != cid.ID  {
			continue
		}
		return c.State
	}
	for _, c := range pod.Sandboxes {
		if c.ID.ID != cid.ID  {
			continue
		}
		return c.State
	}
	return kubecontainer.ContainerStateUnknown
}

func computeEventType(oldState, curState kubecontainer.ContainerState, pod *kubecontainer.Pod, c kubecontainer.ContainerID) *PodLifecycleEvent {
	if oldState == curState {
		return nil
	}
	switch curState {
	case kubecontainer.ContainerStateCreated:
	case kubecontainer.ContainerStateRunning:
		return &PodLifecycleEvent{ID: pod.ID, Type: ContainerStarted, Data: c.ID}
	case kubecontainer.ContainerStateExited:
		return &PodLifecycleEvent{ID: pod.ID, Type: ContainerDied, Data: c.ID}
	default:
		panic(fmt.Sprintf("unrecognized container state: %v", curState))
	}
	return nil
}

func generateContainerState(old, cur *kubecontainer.Pod, c kubecontainer.ContainerID) *PodLifecycleEvent {
	oldState := getContainerState(old, c)
	curState := getContainerState(cur, c)

	return computeEventType(oldState, curState, cur, c)
}

func updateEvents(eventsByPodID map[types.UID][]*PodLifecycleEvent, event *PodLifecycleEvent, pid types.UID) {
	if event == nil {
		return 
	}
	if _, ok := eventsByPodID[pid]; !ok {
		eventsByPodID[pid] = make([]*PodLifecycleEvent, 0)
	}
	eventsByPodID[pid] = append(eventsByPodID[pid], event)
}

func (g *GenericPLEG) relist() {
	g.reListLock.Lock()
	defer g.reListLock.Unlock()

	klog.Infof("GenericPLEG: Relisting")
	pods, err := g.runtime.GetPods(true)
	if err != nil {
		klog.Errorf("GenericPLEG: Unable to retrieve pods: %v", err)
		return
	}

	g.podRecords.setCurrent(pods)

	eventsByPodID := make(map[types.UID][]*PodLifecycleEvent)
	for pid := range g.podRecords {
		old := g.podRecords.getOld(pid)
		cur := g.podRecords.getCurrent(pid)

		containers := getContainersFromPod(old, cur)
		for _, c := range containers {
			event := generateContainerState(old, cur, c)
			updateEvents(eventsByPodID, event, pid)
		}
	}

	for _, pod := range pods {
		//pretty_pod, _ := json.MarshalIndent(pod, "", "\t")
		//fmt.Printf("relist pod : %v\n", string(pretty_pod))

		pid := pod.ID
		pod := g.podRecords.getCurrent(pid)
		podstatus, err := g.runtime.GetPodStatus(pid, pod.Name, pod.Namespace)
		//pretty_podstatus, _ := json.MarshalIndent(podstatus, "", "\t")
		if err != nil {
			klog.Warningf("error get pod status: %v\n", err)
		} else {
			//fmt.Printf("========>setting podStatus: %v\n", string(pretty_podstatus))
			g.cache.Set(pid, podstatus, nil, time.Now())
		}
	}

	for pid, events := range eventsByPodID {


		for i := range events {
			fmt.Printf("===>pid: %v cid: %v, event: %v\n", pid, events[i].Data, events[i].Type)
			g.eventChannel <- events[i]
		}
	}
}

func (g *GenericPLEG) Watch() chan *PodLifecycleEvent {
	return g.eventChannel
}

