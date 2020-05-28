package sliceutils

import (
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
)

func StringInSlice(s string, list []string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

type ByImageSize 	[]kubecontainer.Image

func (a ByImageSize) Less(i, j int) bool {
	return a[i].Size > a[j].Size
}

func (a ByImageSize) Len() int {return len(a)}

func (a ByImageSize) Swap(i, j int) {a[i], a[j] = a[j], a[i]}
