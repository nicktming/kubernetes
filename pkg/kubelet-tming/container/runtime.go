package container

import (
	"k8s.io/apimachinery/pkg/types"
)

type Image struct {
	ID 		string
	RepoTags	[]string
	RepoDigests	[]string
	Size 		int64
}

type ImageSpec struct {
	Image 		string
}

type ImageService interface {

	GetImageRef(image ImageSpec) (string, error)

	ListImages() ([]Image, error)
}

type ContainerState string

const (
	ContainerStateCreated 	ContainerState 	= 	"created"
	ContainerStateRunning 	ContainerState 	= 	"running"
	ContainerStateExited 	ContainerState 	= 	"exited"

	ContainerStateUnknown 	ContainerState 	= 	"unknown"
)

type ContainerID struct {
	Type 		string
	ID 		string
}

type Container struct {
	ID 		ContainerID
	Name 		string
	Image 		string
	ImageID 	string
	Hash 		uint64
	State 		ContainerState
}

type Pod struct {
	ID 		types.UID
	Name 		string
	Namespace 	string
	Containers 	[]*Container
	Sandboxes 	[]*Container
}

type Runtime interface {
	ImageService

	GetPods(all bool) ([]*Pod, error)
}