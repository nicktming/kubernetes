package kuberuntime


import (
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	"k8s.io/klog"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/util/parsers"
	"k8s.io/kubernetes/pkg/credentialprovider"
	credentialprovidersecrets "k8s.io/kubernetes/pkg/credentialprovider/secrets"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)


// PullImage pulls an image from the network to local storage using the supplied
// secrets if necessary.
func (m *kubeGenericRuntimeManager) PullImage(image kubecontainer.ImageSpec, pullSecrets []v1.Secret, podSandboxConfig *runtimeapi.PodSandboxConfig) (string, error) {
	img := image.Image
	repoToPull, _, _, err := parsers.ParseImageName(img)
	if err != nil {
		return "", err
	}

	keyring, err := credentialprovidersecrets.MakeDockerKeyring(pullSecrets, m.keyring)
	if err != nil {
		return "", err
	}

	imgSpec := &runtimeapi.ImageSpec{Image: img}
	creds, withCredentials := keyring.Lookup(repoToPull)
	if !withCredentials {
		klog.V(3).Infof("Pulling image %q without credentials", img)

		imageRef, err := m.imageService.PullImage(imgSpec, nil, podSandboxConfig)
		if err != nil {
			klog.Errorf("Pull image %q failed: %v", img, err)
			return "", err
		}

		return imageRef, nil
	}

	var pullErrs []error
	for _, currentCreds := range creds {
		authConfig := credentialprovider.LazyProvide(currentCreds, repoToPull)
		auth := &runtimeapi.AuthConfig{
			Username:      authConfig.Username,
			Password:      authConfig.Password,
			Auth:          authConfig.Auth,
			ServerAddress: authConfig.ServerAddress,
			IdentityToken: authConfig.IdentityToken,
			RegistryToken: authConfig.RegistryToken,
		}

		imageRef, err := m.imageService.PullImage(imgSpec, auth, podSandboxConfig)
		// If there was no error, return success
		if err == nil {
			return imageRef, nil
		}

		pullErrs = append(pullErrs, err)
	}

	return "", utilerrors.NewAggregate(pullErrs)
}


func (m *kubeGenericRuntimeManager) GetImageRef(image kubecontainer.ImageSpec) (string, error) {
	status, err := m.imageService.ImageStatus(&runtimeapi.ImageSpec{Image: image.Image})
	if err != nil {
		klog.Errorf("ImageStatus for image %q failed: %v", image, err)
		return "", err
	}
	if status == nil {
		return "", nil
	}
	return status.Id, nil
}

func (m *kubeGenericRuntimeManager) ListImages() ([]kubecontainer.Image, error) {
	var images []kubecontainer.Image

	allImages, err := m.imageService.ListImages(nil)

	klog.V(2).Infof("allImages: %v", allImages)

	if err != nil {
		//klog.Errorf("ListImages failed: %v", err)
		return nil, err
	}

	for _, img := range allImages {
		images = append(images, kubecontainer.Image{
			ID:		img.Id,
			Size: 		int64(img.Size_),
			RepoTags: 	img.RepoTags,
			RepoDigests: 	img.RepoDigests,
		})
	}

	return images, nil
}

// TODO: Get imagefs stats directly from CRI.
func (m *kubeGenericRuntimeManager) ImageStats() (*kubecontainer.ImageStats, error) {
	allImages, err := m.imageService.ListImages(nil)
	if err != nil {
		klog.Errorf("ListImages failed: %v", err)
		return nil, err
	}
	stats := &kubecontainer.ImageStats{}
	for _, img := range allImages {
		stats.TotalStorageBytes += img.Size_
	}
	return stats, nil
}
