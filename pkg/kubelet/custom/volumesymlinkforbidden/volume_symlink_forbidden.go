package volumesymlinkforbidden


import (
	"os"
	"fmt"
	"k8s.io/klog"
	"strings"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/nfs"
)

type volumeAdmitHandler struct {
	result lifecycle.PodAdmitResult
	mounter mount.Interface
	nfsPlugin volume.VolumePlugin
}

var _ lifecycle.PodAdmitHandler = &volumeAdmitHandler{}

// NewRuntimeAdmitHandler returns a sysctlRuntimeAdmitHandler which checks whether
// the given runtime support sysctls.
func NewVolumeAdmitHandler() (*volumeAdmitHandler, error) {
	plugins := nfs.ProbeVolumePlugins(volume.VolumeConfig{})

	return &volumeAdmitHandler{
		nfsPlugin:  plugins[0],
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

func (w *volumeAdmitHandler) checkVolumeSymlink(pod *v1.Pod) []string {
	errList := make([]string, 0)
	for _, vol := range pod.Spec.Volumes {
		spec := &volume.Spec{
			Volume: &vol,
		}
		if vol.VolumeSource.NFS != nil {
			mounter, err := w.nfsPlugin.NewMounter(spec, pod, volume.VolumeOptions{})
			if err != nil {
				return []string{err.Error()}
			}
			dir := "/tmp/0218"
			if err := mounter.SetUpAt(dir, volume.MounterArgs{}); err != nil {
				return []string{err.Error()}
			}
			err, symlink := isSymlink(vol.HostPath.Path)
			if err != nil || symlink {
				if err == nil {
					err = fmt.Errorf("%v is a symlink which is not allowed.", vol.HostPath.Path)
				}
				errList = append(errList, err.Error())
			}
			umounter, err := w.nfsPlugin.NewUnmounter(pod.Name, pod.UID)
			if err != nil {
				return []string{err.Error()}
			}
			if err := umounter.TearDownAt(dir); err != nil {
				return []string{err.Error()}
			}
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







