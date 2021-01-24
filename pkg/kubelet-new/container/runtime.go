package container


import (
	"k8s.io/api/core/v1"
	//"k8s.io/client-go/util/flowcontrol"
)

type Runtime interface {

	// TODO podStatus
	// Syncs the running pod into the desired pod.
	SyncPod(pod *v1.Pod) PodSyncResult
}