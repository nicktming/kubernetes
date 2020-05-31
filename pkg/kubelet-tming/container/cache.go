package container


import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
)


type Cache interface {
	//Get(types.UID) (*PodStatus, error)
	//Set(types.UID, *PodStatus, error, time.Time)
	//// GetNewerThan is a blocking call that only returns the status
	//// when it is newer than the given time.
	GetNewerThan(types.UID, time.Time) (*PodStatus, error)
	//Delete(types.UID)
	//UpdateTime(time.Time)
}

type data struct {
	// Status of the pod.
	status *PodStatus
	// Error got when trying to inspect the pod.
	err error
	// Time when the data was last modified.
	modified time.Time
}

type subRecord struct {
	time time.Time
	ch   chan *data
}


// cache implements Cache.
type cache struct {
	// Lock which guards all internal data structures.
	lock sync.RWMutex
	// Map that stores the pod statuses.
	pods map[types.UID]*data
	// A global timestamp represents how fresh the cached data is. All
	// cache content is at the least newer than this timestamp. Note that the
	// timestamp is nil after initialization, and will only become non-nil when
	// it is ready to serve the cached statuses.
	timestamp *time.Time
	// Map that stores the subscriber records.
	subscribers map[types.UID][]*subRecord
}

// NewCache creates a pod cache.
func NewCache() Cache {
	return &cache{pods: map[types.UID]*data{}, subscribers: map[types.UID][]*subRecord{}}
}
