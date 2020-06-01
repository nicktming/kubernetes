package kuberuntime


import (
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
	"k8s.io/klog"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

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

	klog.V(5).Infof("allImages: %v", allImages)

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
