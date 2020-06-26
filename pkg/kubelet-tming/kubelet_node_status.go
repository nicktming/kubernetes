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
	"k8s.io/kubernetes/pkg/kubelet-tming/nodestatus"
	"net"
)


func (kl *Kubelet) recordEvent(eventType, event, message string) {
	kl.recorder.Eventf(kl.nodeRef, eventType, event, message)
}

func (kl *Kubelet) recordNodeStatusEvent(eventType, event string) {
	klog.V(2).Infof("Recording %s event message for node %s", event, kl.nodeName)

	kl.recorder.Eventf(kl.nodeRef, eventType, event, "Node %s status is now: %s", kl.nodeName, event)
}

func (kl *Kubelet) defaultNodeStatusFuncs() []func(*v1.Node) error {

	var nodeAddressFunc func() ([]v1.NodeAddress, error)

	// TODO kl.cloud and kl.appArmorValidator

	var setters []func(n *v1.Node) error

	setters = append(setters,
		nodestatus.NodeAddress(kl.nodeIP, kl.nodeIPValidator, kl.hostname, kl.hostnameOverridden, kl.externalCloudProvider, kl.cloud, nodeAddressFunc),
		nodestatus.MachineInfo(string(kl.nodeName), kl.maxPods, kl.podsPerCore, kl.GetCachedMachineInfo, kl.containerManager.GetCapacity,
		//kl.containerManager.GetDevicePluginResourceCapacity,
			kl.containerManager.GetNodeAllocatableReservation, kl.recordEvent),
		nodestatus.VersionInfo(kl.cadvisor.VersionInfo, kl.containerRuntime.Type, kl.containerRuntime.Version),
		nodestatus.GoRuntime(),
		nodestatus.DaemonEndpoints(kl.daemonEndpoints),
		nodestatus.Images(kl.nodeStatusMaxImages, kl.imageManager.GetImageList),
	)

	setters = append(setters,
		nodestatus.MemoryPressureCondition(kl.clock.Now, kl.evictionManager.IsUnderMemoryPressure, kl.recordNodeStatusEvent),
		nodestatus.DiskPressureCondition(kl.clock.Now, kl.evictionManager.IsUnderDiskPressure, kl.recordNodeStatusEvent),
		nodestatus.PIDPressureCondition(kl.clock.Now, kl.evictionManager.IsUnderPIDPressure, kl.recordNodeStatusEvent),
		nodestatus.ReadyCondition(kl.clock.Now, kl.recordNodeStatusEvent),
		)

	return setters
}

// Validate given node IP belongs to the current host
func validateNodeIP(nodeIP net.IP) error {
	// Honor IP limitations set in setNodeStatus()
	if nodeIP.To4() == nil && nodeIP.To16() == nil {
		return fmt.Errorf("nodeIP must be a valid IP address")
	}
	if nodeIP.IsLoopback() {
		return fmt.Errorf("nodeIP can't be loopback address")
	}
	if nodeIP.IsMulticast() {
		return fmt.Errorf("nodeIP can't be a multicast address")
	}
	if nodeIP.IsLinkLocalUnicast() {
		return fmt.Errorf("nodeIP can't be a link-local unicast address")
	}
	if nodeIP.IsUnspecified() {
		return fmt.Errorf("nodeIP can't be an all zeros address")
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip != nil && ip.Equal(nodeIP) {
			return nil
		}
	}
	return fmt.Errorf("Node IP: %q not found in the host's network interfaces", nodeIP.String())
}



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
	kl.volumeManager.MarkVolumesAsReportedInUse(updatedNode.Status.VolumesInUse)
	return nil
}

func (kl *Kubelet) setNodeStatus(node *v1.Node) {
	for i, f := range kl.setNodeStatusFuncs {
		klog.V(5).Infof("Setting node status at position %v", i)
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



