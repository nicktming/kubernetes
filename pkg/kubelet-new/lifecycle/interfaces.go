package lifecycle

import "k8s.io/api/core/v1"

type PodAdmitAttributes struct {
	// the pod to evaluate for admission
	Pod *v1.Pod
	// all pods bound to the kubelet excluding the pod being evaluated
	OtherPods []*v1.Pod
}

// PodAdmitResult provides the result of a pod admission decision.
type PodAdmitResult struct {
	// if true, the pod should be admitted.
	Admit bool
	// a brief single-word reason why the pod could not be admitted.
	Reason string
	// a brief message explaining why the pod could not be admitted.
	Message string
}

// PodAdmitHandler is notified during pod admission.
type PodAdmitHandler interface {
	// Admit evaluates if a pod can be admitted.
	Admit(attrs *PodAdmitAttributes) PodAdmitResult
}

// PodAdmitHandlers maintains a list of handlers to pod admission.
type PodAdmitHandlers []PodAdmitHandler

// AddPodAdmitHandler adds the specified observer.
func (handlers *PodAdmitHandlers) AddPodAdmitHandler(a PodAdmitHandler) {
	*handlers = append(*handlers, a)
}

