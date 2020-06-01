package container


import (
	"sync"
	"time"
)

var (
	// TODO(yifan): Maybe set the them as parameters for NewCache().
	defaultCachePeriod = time.Second * 2
)

type RuntimeCache interface {
	GetPods() ([]*Pod, error)
	ForceUpdateIfOlder(time.Time) error
}

type podsGetter interface {
	GetPods(bool) ([]*Pod, error)
}

// NewRuntimeCache creates a container runtime cache.
func NewRuntimeCache(getter podsGetter) (RuntimeCache, error) {
	return &runtimeCache{
		getter: getter,
	}, nil
}

// runtimeCache caches a list of pods. It records a timestamp (cacheTime) right
// before updating the pods, so the timestamp is at most as new as the pods
// (and can be slightly older). The timestamp always moves forward. Callers are
// expected not to modify the pods returned from GetPods.
type runtimeCache struct {
	sync.Mutex
	// The underlying container runtime used to update the cache.
	getter podsGetter
	// Last time when cache was updated.
	cacheTime time.Time
	// The content of the cache.
	pods []*Pod
}

// GetPods returns the cached pods if they are not outdated; otherwise, it
// retrieves the latest pods and return them.
func (r *runtimeCache) GetPods() ([]*Pod, error) {
	r.Lock()
	defer r.Unlock()
	if time.Since(r.cacheTime) > defaultCachePeriod {
		if err := r.updateCache(); err != nil {
			return nil, err
		}
	}
	return r.pods, nil
}

func (r *runtimeCache) ForceUpdateIfOlder(minExpectedCacheTime time.Time) error {
	r.Lock()
	defer r.Unlock()
	if r.cacheTime.Before(minExpectedCacheTime) {
		return r.updateCache()
	}
	return nil
}

func (r *runtimeCache) updateCache() error {
	pods, timestamp, err := r.getPodsWithTimestamp()
	if err != nil {
		return err
	}
	r.pods, r.cacheTime = pods, timestamp
	return nil
}

// getPodsWithTimestamp records a timestamp and retrieves pods from the getter.
func (r *runtimeCache) getPodsWithTimestamp() ([]*Pod, time.Time, error) {
	// Always record the timestamp before getting the pods to avoid stale pods.
	timestamp := time.Now()
	pods, err := r.getter.GetPods(false)
	return pods, timestamp, err
}
