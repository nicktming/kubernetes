package images


import (
	"time"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
)

type pullResult struct {
	imageRef string
	err      error
}

type imagePuller interface {
	pullImage(kubecontainer.ImageSpec, []v1.Secret, chan<- pullResult, *runtimeapi.PodSandboxConfig)
}

var _, _ imagePuller = &parallelImagePuller{}, &serialImagePuller{}

type parallelImagePuller struct {
	imageService kubecontainer.ImageService
}

func newParallelImagePuller(imageService kubecontainer.ImageService) imagePuller {
	return &parallelImagePuller{imageService}
}

func (pip *parallelImagePuller) pullImage(spec kubecontainer.ImageSpec, pullSecrets []v1.Secret, pullChan chan<- pullResult, podSandboxConfig *runtimeapi.PodSandboxConfig) {
	go func() {
		imageRef, err := pip.imageService.PullImage(spec, pullSecrets, podSandboxConfig)
		pullChan <- pullResult{
			imageRef: imageRef,
			err:      err,
		}
	}()
}

// Maximum number of image pull requests than can be queued.
const maxImagePullRequests = 10

type serialImagePuller struct {
	imageService kubecontainer.ImageService
	pullRequests chan *imagePullRequest
}

func newSerialImagePuller(imageService kubecontainer.ImageService) imagePuller {
	imagePuller := &serialImagePuller{imageService, make(chan *imagePullRequest, maxImagePullRequests)}
	go wait.Until(imagePuller.processImagePullRequests, time.Second, wait.NeverStop)
	return imagePuller
}

type imagePullRequest struct {
	spec             kubecontainer.ImageSpec
	pullSecrets      []v1.Secret
	pullChan         chan<- pullResult
	podSandboxConfig *runtimeapi.PodSandboxConfig
}

func (sip *serialImagePuller) pullImage(spec kubecontainer.ImageSpec, pullSecrets []v1.Secret, pullChan chan<- pullResult, podSandboxConfig *runtimeapi.PodSandboxConfig) {
	sip.pullRequests <- &imagePullRequest{
		spec:             spec,
		pullSecrets:      pullSecrets,
		pullChan:         pullChan,
		podSandboxConfig: podSandboxConfig,
	}
}

func (sip *serialImagePuller) processImagePullRequests() {
	for pullRequest := range sip.pullRequests {
		imageRef, err := sip.imageService.PullImage(pullRequest.spec, pullRequest.pullSecrets, pullRequest.podSandboxConfig)
		pullRequest.pullChan <- pullResult{
			imageRef: imageRef,
			err:      err,
		}
	}
}
