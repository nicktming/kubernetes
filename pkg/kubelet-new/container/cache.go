package container

import (
	//"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"fmt"
)

type Cache interface {
	Get(types.UID) (*PodStatus, error)
	Set(types.UID, *PodStatus, error, time.Time)
	// GetNewerThan is a blocking call that only returns the status
	// when it is newer than the given time.
	//GetNewerThan(types.UID, time.Time) (*PodStatus, error)
	//Delete(types.UID)
	//UpdateTime(time.Time)
}


type cache struct {
	pods map[types.UID]*PodStatus
}

func NewCache() Cache {
	return &cache{
		pods: make(map[types.UID]*PodStatus),
	}
}

func (c *cache) Get(pid types.UID) (*PodStatus, error) {
	ps, ok := c.pods[pid]
	if !ok {
		return &PodStatus{
			ID: pid,
		}, nil
	}
	return ps, nil
}

func (c *cache) Set(pid types.UID, ps *PodStatus, err error, t time.Time) {
	c.pods[pid] = ps
}