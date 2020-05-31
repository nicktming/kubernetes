package types

import (
	"k8s.io/apimachinery/pkg/types"
	"time"
)

type Timestamp struct {
	time	time.Time
}

func NewTimestamp() *Timestamp {
	return &Timestamp{time.Now()}
}


func ConvertToTimestamp(timeString string) *Timestamp {
	parsed, _ := time.Parse(time.RFC3339Nano, timeString)
	return &Timestamp{parsed}
}

func (t *Timestamp) Get() time.Time {
	return t.time
}

func (t *Timestamp) GetString() string {
	return t.time.Format(time.RFC3339Nano)
}

// A pod UID which has been translated/resolved to the representation known to kubelets.
type ResolvedPodUID types.UID

// A pod UID for a mirror pod.
type MirrorPodUID types.UID
