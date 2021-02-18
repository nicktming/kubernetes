package volumesymlinkforbidden


import (
	"os"
	"fmt"
	"k8s.io/klog"
	"strings"
	"k8s.io/kubernetes/pkg/kubelet-new/lifecycle"
)

type volumeAdmitHandler struct {
	result lifecycle.PodAdmitResult
}

var _ lifecycle.PodAdmitHandler = &volumeAdmitHandler{}

// NewRuntimeAdmitHandler returns a sysctlRuntimeAdmitHandler which checks whether
// the given runtime support sysctls.
func NewVolumeAdmitHandler() (*volumeAdmitHandler, error) {
	return &volumeAdmitHandler{}, nil
}

// Admit checks whether the runtime supports sysctls.
func (w *volumeAdmitHandler) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	pod := attrs.Pod
	errList := make([]string, 0)
	for _, vol := range pod.Spec.Volumes {
		if vol.HostPath != nil {
			err, symlink := volumeSymlink(vol.HostPath.Path)
			if err != nil || symlink {
				if err == nil {
					err = fmt.Errorf("%v is a symlink, it is not allowed.", vol.HostPath.Path)
				}
				errList = append(errList, err.Error())
			}
		}
	}

	return lifecycle.PodAdmitResult{
		Admit: len(errList) == 0,
		Reason: "this version is not allowed to mount symlink",
		Message: strings.Join(errList, "\n"),
	}
}

func volumeSymlink(path string) (error, bool) {
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return err, false
	}
	klog.Infof("Link info: %+v", fileInfo)
	if (fileInfo.Mode() & os.ModeSymlink) != 0 {
		return nil, true
	}
	return err, false
}







