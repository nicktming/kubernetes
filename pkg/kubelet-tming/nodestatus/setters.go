package nodestatus

import (
	"k8s.io/api/core/v1"
	"net"
	"k8s.io/cloud-provider"
	"k8s.io/klog"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	goruntime "runtime"
	"time"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	"fmt"
)

const (
	MaxNamesPerImageInNodeStatus = 5
)

type Setter func(node *v1.Node) error


func Images(nodeStatusMaxImages int32,
		imageListFunc func() ([]kubecontainer.Image, error)) Setter {
	return func(node *v1.Node) error {
		var imagesOnNode []v1.ContainerImage
		containerImages, err := imageListFunc()
		if err != nil {
			klog.Errorf("Error getting image list: %v", err)
			node.Status.Images = imagesOnNode
			return fmt.Errorf("error getting image list: %v", err)
		}

		if int(nodeStatusMaxImages) > -1 &&
			int(nodeStatusMaxImages) < len(containerImages) {
			containerImages = containerImages[0:nodeStatusMaxImages]
		}

		for _, image := range containerImages {
			names := append(image.RepoDigests, image.RepoTags...)
			if len(names) > MaxNamesPerImageInNodeStatus {
				names = names[0:MaxNamesPerImageInNodeStatus]
			}
			imagesOnNode = append(imagesOnNode, v1.ContainerImage{
				Names: 		names,
				SizeBytes: 	image.Size,
			})
		}
		node.Status.Images = imagesOnNode
		return nil
	}
}

func MemoryPressureCondition(nowFunc func() time.Time,
			pressureFunc func() bool,
			recordEventFunc func(eventType, event string),
) Setter {
	return func(node *v1.Node) error {
		currentTime := metav1.NewTime(nowFunc())

		var condition *v1.NodeCondition

		for i := range node.Status.Conditions {
			if node.Status.Conditions[i].Type == v1.NodeMemoryPressure {
				condition = &node.Status.Conditions
			}
		}

		newCondition := false
		if condition == nil {
			condition = &v1.NodeCondition {
				Type: 		v1.NodeMemoryPressure,
				Status: 	v1.ConditionUnknown,
			}
			newCondition = true
		}

		condition.LastHeartbeatTime = currentTime

		if pressureFunc() {
			if condition.Status != v1.ConditionTrue {
				condition.Status = v1.ConditionTrue
				condition.Reason = "KubeletHasInsufficientMemory"
				condition.Message = "kubelet has insufficient memory available"
				recordEventFunc(v1.EventTypeNormal, "NodeHasInsufficientMemory")
			}
		} else if condition.Status != v1.ConditionFalse {
			condition.Status = v1.ConditionFalse
			condition.Reason = "KubeletHasSufficientMemory"
			condition.Message = "kubelet has sufficient memory avaiable"
			condition.LastTransitionTime = currentTime
			recordEventFunc(v1.EventTypeNormal, "NodeHasSufficientMemory")
		}

		if newCondition {
			node.Status.Conditions = append(node.Status.Conditions, *condition)
		}

		return nil
	}
}


func GoRuntime() Setter {
	return func(node *v1.Node) error {
		node.Status.NodeInfo.OperatingSystem = goruntime.GOOS
		node.Status.NodeInfo.Architecture = goruntime.GOARCH
		return nil
	}
}

func NodeAddress(nodeIP net.IP,
		validateNodeIPFunc func(net.IP) error,
		hostname string,
		hostnameOverridden bool,
		externalCloudProvider bool,
		cloud cloudprovider.Interface,
		nodeAddressesFunc func() ([]v1.NodeAddress, error),
) Setter {
	return func(node *v1.Node) error {
		if nodeIP != nil {
			klog.Infof("Using node IP: %q", nodeIP.String())
		}
		if cloud != nil {
			// TODO
		} else {
			var ipAddr net.IP
			var err error

			if nodeIP != nil {
				ipAddr = nodeIP
			} else if addr := net.ParseIP(hostname); addr != nil {
				ipAddr = addr
			} else {
				var addrs []net.IP
				addrs, _ = net.LookupIP(node.Name)
				for _, addr := range addrs {
					if err = validateNodeIPFunc(addr); err == nil {
						if addr.To4() != nil {
							ipAddr = addr
							break
						}
						if addr.To16() != nil && ipAddr == nil {
							ipAddr = addr
						}
					}
				}

				if ipAddr == nil {
					ipAddr, err = utilnet.ChooseHostInterface()
				}

				node.Status.Addresses = []v1.NodeAddress {
					{Type: v1.NodeInternalIP, Address: ipAddr.String()},
					{Type: v1.NodeHostName, Address: hostname},
				}
			}
		}
		return nil
	}
}
