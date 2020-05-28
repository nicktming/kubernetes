package images

import (
	"errors"

	"k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

var (
	ErrImagePullBackOff	= errors.New("ImagePullBackOff")

	ErrImageInspect		= errors.New("ImageInspectError")

	ErrImagePull		= errors.New("ErrImagePull")

	ErrImageNeverPull = errors.New("ErrImageNeverPull")

	ErrRegistryUnavailable = errors.New("RegistryUnavailable")

	ErrInvalidImageName = errors.New("InvalidImageName")
)


type ImageManager interface {
	// EnsureImageExists ensures that image specified in `container` exists.
	EnsureImageExists(pod *v1.Pod, container *v1.Container, pullSecrets []v1.Secret, podSandboxConfig *runtimeapi.PodSandboxConfig) (string, string, error)

	// TODO(ronl): consolidating image managing and deleting operation in this interface
}
