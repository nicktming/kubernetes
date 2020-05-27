package kubelet_tming


import (
	v1 "k8s.io/api/core/v1"
	"time"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeletapis "k8s.io/kubernetes/pkg/kubelet/apis"
	goruntime "runtime"
	"k8s.io/klog"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/kubernetes/pkg/kubelet/util"
	"fmt"
	nodeutil "k8s.io/kubernetes/pkg/util/node"
	"k8s.io/apimachinery/pkg/types"
)

func (kl *Kubelet) initialNode() (*v1.Node, error) {
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: string(kl.nodeName),
			Labels: map[string]string{
				v1.LabelHostname:      kl.hostname,
				v1.LabelOSStable:      goruntime.GOOS,
				v1.LabelArchStable:    goruntime.GOARCH,
				kubeletapis.LabelOS:   goruntime.GOOS,
				kubeletapis.LabelArch: goruntime.GOARCH,
			},
		},
		Spec: v1.NodeSpec{
			Unschedulable: !kl.registerSchedulable,
		},
	}
	kl.setNodeStatus(node)
	return node, nil
}


func (kl *Kubelet) tryUpdateNodeStatus(tryNumber int) error {
	opts := metav1.GetOptions{}
	if tryNumber == 0 {
		util.FromApiserverCache(&opts)
	}
	node, err := kl.heartbeatClient.CoreV1().Nodes().Get(string(kl.nodeName), opts)
	if err != nil {
		return fmt.Errorf("error getting node %q: %v", kl.nodeName, err)
	}

	originalNode := node.DeepCopy()
	if originalNode == nil {
		return fmt.Errorf("nil %q node object", kl.nodeName)
	}

	// TODO CIDR
	kl.setNodeStatus(node)

	now := kl.clock.Now()

	updatedNode, _, err := nodeutil.PatchNodeStatus(kl.heartbeatClient.CoreV1(), types.NodeName(kl.nodeName), originalNode, node)
	if err != nil {
		return err
	}
	kl.lastStatusReportTime = now

	kl.setLastObservedNodeAddresses(updatedNode.Status.Addresses)

	return nil
}

func (kl *Kubelet) setNodeStatus(node *v1.Node) {
	for i, f := range kl.setNodeStatusFuncs {
		klog.Infof("Setting node status at position %v", i)
		if err := f(node); err != nil {
			klog.Warningf("Failed to set some node status filelds: %s", err)
		}
	}
}

func (kl *Kubelet) registerWithAPIServer() {
	if kl.registrationCompleted {
		return
	}

	step := 100 * time.Millisecond

	for {
		time.Sleep(step)
		step = step * 2

		if step >= 7 * time.Second {
			step = 7 * time.Second
		}

		node, err := kl.initialNode()
		if err != nil {
			klog.Errorf("Unable to construct v1.Node object for kubelet: %v", err)
			continue
		}

		klog.Infof("Attempting to register node %s", node.Name)

		registered := kl.tryRegisterWithAPIServer(node)
		if registered {
			klog.Infof("Successfully registered node %s", node.Name)
			kl.registrationCompleted = true
			return
		}
	}
}

func (kl *Kubelet) tryRegisterWithAPIServer(node *v1.Node) bool {
	_, err := kl.kubeClient.CoreV1().Nodes().Create(node)
	if err == nil {
		return true
	}
	if !apierrors.IsAlreadyExists(err) {
		klog.Errorf("Unable to register node %q with API server: %v", kl.nodeName, err)
		return false
	}
	existingNode, err := kl.kubeClient.CoreV1().Nodes().Get(string(kl.nodeName), metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Unable to register node %q with API server: error getting existing node: %v", kl.nodeName, err)
		return false
	}
	if existingNode == nil {
		klog.Errorf("Unable to register node %q with API server: no node instance returned", kl.nodeName)
		return false
	}
	originalNode := existingNode.DeepCopy()
	if originalNode == nil {
		klog.Errorf("Nil %q node object", kl.nodeName)
		return false
	}
	klog.Infof("Node %s was previously registered", kl.nodeName)

	// TODO reconcile something here

	return true
}


