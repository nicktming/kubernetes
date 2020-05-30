package cadvisor

import (
	"github.com/google/cadvisor/events"
	cadvisorapi "github.com/google/cadvisor/info/v1"
	cadvisorapiv2 "github.com/google/cadvisor/info/v2"
)


type Interface interface {


	Start() error

	DockerContainer(name string, req *cadvisorapi.ContainerInfoRequest) (cadvisorapi.ContainerInfo, error)

	ContainerInfo(name string, req *cadvisorapi.ContainerInfoRequest) (*cadvisorapi.ContainerInfo, error)

	ContainerInfoV2(name string, options cadvisorapiv2.RequestOptions) (map[string]cadvisorapiv2.ContainerInfo, error)

	SubcontainerInfo(name string, req *cadvisorapi.ContainerInfoRequest) (map[string]*cadvisorapi.ContainerInfo, error)

	MachineInfo() (*cadvisorapi.MachineInfo, error)

	VersionInfo() (*cadvisorapi.VersionInfo, error)

	ImagesFsInfo() (cadvisorapiv2.FsInfo, error)

	RootFsInfo() (cadvisorapiv2.FsInfo, error)

	WatchEvents(request *events.Request) (*events.EventChannel, error)

	GetDirFsInfo(path string) (cadvisorapiv2.FsInfo, error)

}

type ImageFsInfoProvider interface {

	ImageFsInfoLabel() (string, error)

}
