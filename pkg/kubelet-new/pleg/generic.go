package pleg

import (
	"k8s.io/apimachinery/pkg/util/wait"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	"time"
	"k8s.io/klog"
	"gopkg.in/square/go-jose.v2/json"
	"fmt"
)

type GenericPLEG struct {
	// The period for relisting.
	relistPeriod time.Duration

	runtime kubecontainer.Runtime
}

func NewGenericPLEG(runtime kubecontainer.Runtime, relistPeriod time.Duration) PodLifecycleEventGenerator {
	return &GenericPLEG{
		relistPeriod: relistPeriod,
		runtime: runtime,
	}
}


func (g *GenericPLEG) Start() {
	go wait.Until(g.relist, g.relistPeriod, wait.NeverStop)
}

func (g *GenericPLEG) relist() {
	klog.Infof("GenericPLEG: Relisting")
	pods, err := g.runtime.GetPods(true)
	if err != nil {
		klog.Errorf("GenericPLEG: Unable to retrieve pods: %v", err)
		return
	}

	for _, pod := range pods {
		pretty_pod, _ := json.MarshalIndent(pod, "", "\t")
		fmt.Printf("relist pod : %v\n", string(pretty_pod))
	}
}
