package volumesymlinkforbidden


import (
	"os"
	"fmt"
	"k8s.io/klog"
	"strings"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/api/core/v1"
)

type volumeAdmitHandler struct {
	result lifecycle.PodAdmitResult
	mounter mount.Interface
}

var _ lifecycle.PodAdmitHandler = &volumeAdmitHandler{}

// NewRuntimeAdmitHandler returns a sysctlRuntimeAdmitHandler which checks whether
// the given runtime support sysctls.
func NewVolumeAdmitHandler(mounter mount.Interface) (*volumeAdmitHandler, error) {
	return &volumeAdmitHandler{
		mounter: 		mounter,
	}, nil
}

// Admit checks whether the runtime supports sysctls.
func (w *volumeAdmitHandler) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	pod := attrs.Pod
	errList := w.checkVolumeSymlink(pod)

	return lifecycle.PodAdmitResult{
		Admit: len(errList) == 0,
		Reason: "UnexpectedAdmissionError",
		Message: strings.Join(errList, "\n"),
	}
}

func (w *volumeAdmitHandler) cleanupNfs(dir string) error {
	notMnt, mntErr := mount.IsNotMountPoint(w.mounter, dir)
	if mntErr != nil {
		klog.Errorf("IsNotMountPoint check failed: %v", mntErr)
		return mntErr
	}
	if !notMnt {
		if mntErr = w.mounter.Unmount(dir); mntErr != nil {
			klog.Errorf("Failed to unmount: %v", mntErr)
			return mntErr
		}
		notMnt, mntErr := mount.IsNotMountPoint(w.mounter, dir)
		if mntErr != nil {
			klog.Errorf("IsNotMountPoint check failed: %v", mntErr)
			return mntErr
		}
		if !notMnt {
			// This is very odd, we don't expect it.  We'll try again next sync loop.
			klog.Errorf("%s is still mounted, despite call to unmount().  Will try again next sync loop.", dir)
			return mntErr
		}
	}
	os.Remove(dir)
	return nil
}

func (w *volumeAdmitHandler) checkVolumeSymlink(pod *v1.Pod) []string {
	errList := make([]string, 0)
	for _, vol := range pod.Spec.Volumes {
		if vol.VolumeSource.NFS != nil {
			source := fmt.Sprintf("%s:%s", vol.VolumeSource.NFS.Server, vol.VolumeSource.NFS.Path)
			dir := "/tmp/0218"
			mountOptions := []string{}
			err := w.mounter.Mount(source, dir, "nfs", mountOptions)
			if err != nil {
				w.cleanupNfs(dir)
				return []string{err.Error()}
			}
			err, symlink := isSymlink(dir)
			if err != nil || symlink {
				if err == nil {
					err = fmt.Errorf("%v is a symlink which is not allowed.", vol.HostPath.Path)
				}
				errList = append(errList, err.Error())
			}
			w.cleanupNfs(dir)
		} else if vol.VolumeSource.HostPath != nil {
			err, symlink := isSymlink(vol.HostPath.Path)
			if err != nil || symlink {
				if err == nil {
					err = fmt.Errorf("%v is a symlink which is not allowed.", vol.HostPath.Path)
				}
				errList = append(errList, err.Error())
			}
		}
	}
	return errList
}

func isSymlink(path string) (error, bool) {
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







