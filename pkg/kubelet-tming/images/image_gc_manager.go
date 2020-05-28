package images

import (
	"k8s.io/kubernetes/pkg/kubelet-tming/container"
	"time"
	statsapi "k8s.io/kubernetes/pkg/kubelet/apis/stats/v1alpha1"
	"k8s.io/client-go/tools/record"
	"k8s.io/api/core/v1"
	"sync"
	"sort"
	"k8s.io/kubernetes/pkg/kubelet-tming/util/sliceutils"
	"fmt"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
	"k8s.io/apimachinery/pkg/util/wait"
)

type ImageGCManager interface {

	GetImageList() ([]container.Image, error)

	Start()

}

type StatsProvider interface {
	// ImageFsStats returns the stats of the image filesystem.
	ImageFsStats() (*statsapi.FsStats, error)
}


type ImageGCPolicy struct {
	HighThresholdPercent int

	LowThresholdPercent int

	MinAge time.Duration
}

type imageRecord struct {

	firstDetected 		time.Time

	lastUsed 		time.Time

	size			int64
}

type imageCache struct {

	sync.RWMutex

	images 	[]container.Image
}

func (i *imageCache) set(images []container.Image) {
	i.Lock()
	defer i.Unlock()
	i.images = images
}

func (i *imageCache) get() []container.Image {
	i.Lock()
	defer i.Unlock()
	sort.Sort(sliceutils.ByImageSize(i.images))
	return i.images
}


type realImageGCManager struct {

	runtime 		container.Runtime

	imageRecords		map[string]*imageRecord
	imageRecordsLock 	sync.Mutex

	policy 			ImageGCPolicy

	statsProvider		StatsProvider

	recorder 		record.EventRecorder

	nodeRef			*v1.ObjectReference

	initialized 		bool

	imageCache		imageCache

	sandboxImage 		string

}


func NewImageGCManager(runtime container.Runtime, recorder record.EventRecorder, nodeRef *v1.ObjectReference, policy ImageGCPolicy, sandboxImage string) (ImageGCManager, error) {
	if policy.HighThresholdPercent < 0 || policy.HighThresholdPercent > 100 {
		return nil, fmt.Errorf("invalid HighThresholdPercent %d, must be in range [0-100]", policy.HighThresholdPercent)
	}
	if policy.LowThresholdPercent < 0 || policy.LowThresholdPercent > 100 {
		return nil, fmt.Errorf("invalid LowThresholdPercent %d, must be in range [0-100]", policy.LowThresholdPercent)
	}
	if policy.LowThresholdPercent > policy.HighThresholdPercent {
		return nil, fmt.Errorf("LowThresholdPercent %d can not be higher than HighThresholdPercent %d", policy.LowThresholdPercent, policy.HighThresholdPercent)
	}

	im := &realImageGCManager{
		runtime: 	runtime,
		policy:		policy,
		imageRecords:	make(map[string]*imageRecord),
		// TODO
		// statsProvider:  statsProvider,
		recorder: 	recorder,
		nodeRef: 	nodeRef,
		initialized:    false,
		sandboxImage: 	sandboxImage,
	}

	return im, nil
}


func (im *realImageGCManager) Start() {
	go wait.Until(func(){
		var ts time.Time
		if im.initialized {
			ts = time.Now()
		}
		_, err := im.detectImages(ts)
		if err != nil {
			klog.Warningf("[imageGCManager] Failed to monitor images: %v", err)
		} else {
			im.initialized = true
		}
	}, 5 * time.Minute, wait.NeverStop)

	go wait.Until(func(){
		images, err := im.runtime.ListImages()
		klog.V(5).Infof("images: %v", images)
		if err != nil {
			klog.Warningf("[imageGCManager] Failed to update image list: %v", err)
		} else {
			im.imageCache.set(images)
		}
	}, 30 * time.Second, wait.NeverStop)
}


func (im *realImageGCManager) GetImageList() ([]container.Image, error) {
	return im.imageCache.get(), nil
}

func (im *realImageGCManager) detectImages(detectTime time.Time) (sets.String, error) {
	imagesInUse := sets.NewString()

	imageRef, err := im.runtime.GetImageRef(container.ImageSpec{Image: im.sandboxImage})

	if err == nil && imageRef != "" {
		imagesInUse.Insert(imageRef)
	}

	images, err := im.runtime.ListImages()
	if err != nil {
		return imagesInUse, err
	}

	pods, err := im.runtime.GetPods(true)
	if err != nil {
		return imagesInUse, err
	}

	for _, pod := range pods {
		for _, container := range pod.Containers {
			klog.Infof("Pod %s/%s, container %s uses image %s(%s)", pod.Namespace, pod.Name, container.Name, container.Image, container.ImageID)
			imagesInUse.Insert(container.ImageID)
		}
	}

	now := time.Now()
	currentImages := sets.NewString()

	im.imageRecordsLock.Lock()
	defer im.imageRecordsLock.Unlock()

	for _, image := range images {
		klog.Infof("Adding image ID %s to currentImages", image.ID)
		currentImages.Insert(image.ID)

		if _, ok := im.imageRecords[image.ID]; !ok {
			klog.Infof("Image ID %s is new", image.ID)
			im.imageRecords[image.ID] = &imageRecord {
				firstDetected: detectTime,
			}
		}

		if isImageUsed(image.ID, imagesInUse) {
			klog.Infof("Setting Image ID %s lastUsed to %v", image.ID, now)
			im.imageRecords[image.ID].lastUsed = now
		}

		klog.Infof("Image ID %s has size %d", image.ID, image.Size)
		im.imageRecords[image.ID].size = image.Size
	}

	for image := range im.imageRecords {
		if !currentImages.Has(image) {
			klog.Infof("Image ID is no longer present; removing from imageRecords", image)
			delete(im.imageRecords, image)
		}
	}

	return imagesInUse, nil
}

func isImageUsed(imageID string, imagesInUse sets.String) bool {
	// Check the image ID.
	if _, ok := imagesInUse[imageID]; ok {
		return true
	}
	return false
}
















































































