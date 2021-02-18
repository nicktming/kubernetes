package lifecycle


type volumeAdmitHandler struct {
	result PodAdmitResult
}

var _ PodAdmitHandler = &volumeAdmitHandler{}

// NewRuntimeAdmitHandler returns a sysctlRuntimeAdmitHandler which checks whether
// the given runtime support sysctls.
func NewRuntimeAdmitHandler() (*volumeAdmitHandler, error) {
	return &volumeAdmitHandler{}, nil
}

// Admit checks whether the runtime supports sysctls.
func (w *volumeAdmitHandler) Admit(attrs *PodAdmitAttributes) PodAdmitResult {
	if attrs.Pod.Spec.SecurityContext != nil {

		if len(attrs.Pod.Spec.SecurityContext.Sysctls) > 0 {
			return w.result
		}
	}

	return PodAdmitResult{
		Admit: true,
	}
}



