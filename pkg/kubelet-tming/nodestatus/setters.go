package nodestatus

import (
	"k8s.io/api/core/v1"
	"net"
	"k8s.io/cloud-provider"
	"k8s.io/klog"
	utilnet "k8s.io/apimachinery/pkg/util/net"
)

const (
	MaxNamesPerImageInNodeStatus = 5
)

type Setter func(node *v1.Node) error


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