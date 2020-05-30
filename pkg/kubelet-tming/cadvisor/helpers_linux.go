package cadvisor

import (
	cadvisorfs "github.com/google/cadvisor/fs"
	"k8s.io/kubernetes/pkg/kubelet-tming/types"
	"fmt"
)

type imageFsInfoProvider struct {
	runtime 		string
	runtimeEndpoint 	string
}

func (i *imageFsInfoProvider) ImageFsInfoLabel() (string, error) {
	switch i.runtime {
	case types.DockerContainerRuntime:
		return cadvisorfs.LabelDockerImages, nil

	// TODO remote
	}

	return "", fmt.Errorf("no imagefs label for configured runtime")
}

func NewImageFsInfoProvider(runtime, runtimeEndpoint string) ImageFsInfoProvider {
	return &imageFsInfoProvider{
		runtime: 		runtime,
		runtimeEndpoint: 	runtimeEndpoint,
	}
}
