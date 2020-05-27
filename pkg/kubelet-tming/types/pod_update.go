package types

import (
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConfigSourceAnnotationKey    = "kubernetes.io/config.source"
	ConfigMirrorAnnotationKey    = v1.MirrorPodAnnotationKey
	ConfigFirstSeenAnnotationKey = "kubernetes.io/config.seen"
	ConfigHashAnnotationKey      = "kubernetes.io/config.hash"
	CriticalPodAnnotationKey     = "scheduler.alpha.kubernetes.io/critical-pod"
)

type PodOperation int

const (
	SET 		PodOperation = iota
	ADD
	DELETE
	REMOVE
	UPDATE
	RECONCILE
	RESTORE

	FileSource = "file"
	HTTPSource = "http"
	ApiserverSource = "api"
	AllSource = "*"

	NamespaceDefault = metav1.NamespaceDefault
)

type PodUpdate struct {
	Pods 		[]*v1.Pod
	Op		PodOperation
	Source 		string
}