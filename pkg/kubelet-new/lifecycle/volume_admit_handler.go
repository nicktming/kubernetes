package lifecycle

import (
	"os"
	"fmt"
	"k8s.io/klog"
	"strings"
)

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
	pod := attrs.Pod
	errList := make([]string, 0)
	for _, vol := range pod.Spec.Volumes {
		if vol.HostPath != nil {
			err, symlink := volumeSymlink(vol.HostPath.Path)
			if err != nil || symlink {
				if err != nil {
					err = fmt.Sprintf("%v is a symlink, it is not allowed.", vol.HostPath.Path)
				}
				errList = append(errList, err.Error())
			}
		}
	}

	return PodAdmitResult{
		Admit: len(errList) == 0,
		Reason: "this version is not allowed to mount symlink",
		Message: strings.Join(errList, "\n"),
	}
}

func volumeSymlink(path string) (error, bool) {
	fileInfo, err := os.Lstat("original_sym.txt")
	if err != nil {
		return err, false
	}
	klog.Infof("Link info: %+v", fileInfo)
	if (fileInfo.Mode() & os.ModeSymlink) != 0 {
		return nil, true
	}
	return err, false
}






