package symlinkforbidden

import (
	"fmt"
	"strings"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/api/core/v1"
	"time"
	"os"
)

type symlinkForbidden struct {
	mountTimeout time.Duration 
	umountTimeout time.Duration
}

type mountFunc func(source, dst, fstype string, timeout time.Duration) error
type umountFunc func(dst string, timeout time.Duration)

var (
	_ lifecycle.PodAdmitHandler = &symlinkForbidden{}
	nfsErr = "Program not registered"
	rpcErr = "Port mapper failure"
	basepath = "/tmp/volumesymlinkforbidden"
	nfsMountFunc mountFunc = mountWithTimeout
	nfsUmountFunc umountFunc = umountWithTimeout
	SYMLINKFORBIDDENREASON                           = "SymlinkForbidden"
)

// NOTICE: ONLY SUPPORT NFS and HOSTPATH now.

// NewSymlinkForbidden returns a symlinkForbidden which checks whether
// the given pods using symlink volumes. 
func NewSymlinkForbidden(mountTimeout, umountTimeout time.Duration) (*symlinkForbidden, error) {
	_, err := os.Stat(basepath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err 
		}
		if err := os.Mkdir(basepath, 0750); err != nil {
			return nil, err
		}
	}
	sf :=  &symlinkForbidden{
		mountTimeout: 	mountTimeout,
		umountTimeout: 	umountTimeout,
	}
	go cleanup(umountTimeout, nfsUmountFunc)
	return sf, nil 
}

func (w *symlinkForbidden) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	pod := attrs.Pod
	errList := w.checkVolumeSymlink(pod)
	return lifecycle.PodAdmitResult{
		Admit: len(errList) == 0,
		Reason: SYMLINKFORBIDDENREASON,
		Message: strings.Join(errList, "\n"),
	}
}

/**
for nfs type:
	problem: it is not a symlink when mouting a nfs symlink to node. so it is necessary to mount its root path.
	there will be two steps:
	1. Find its root path which comes from 'showmount -e ${ip}'.
	2. Mouting its root path to node.
	3. Check whether its corrsponding nfs path is symlink.

	example:
	pod volume such as: ${ip}:/mnt/nfs/sharelink
	1. showmount will find some root path of this ip: /mnt/nfs
	2. the mount command will be like: mount -t nfs ${ip}:/mnt/nfs /tmp/nfs123
	3. checkPath: /tmp/nfs123/sharelink 
for hostpath type:
	only check whether its path is symlink
**/
func (sf *symlinkForbidden) checkVolumeSymlink(pod *v1.Pod) []string {
	errList := make([]string, 0)
	for _, vol := range pod.Spec.Volumes {
		if vol.VolumeSource.NFS != nil {
			server := vol.VolumeSource.NFS.Server
			path := vol.VolumeSource.NFS.Path
			// 1. find all the root path using 'showmount -e'
			serverpaths, err := findNFSServerPaths(server)
			if err != nil {
				return []string{err.Error()}
			}
			found := false 
			for _, serverpath := range serverpaths {
				if serverpath != "" && strings.Contains(path, serverpath) {
					found = true 
					// 2. find the corrsponding server path, and mount it to node, then return the absolute path at node
					checkPath, _, err := prepareNFSCheckPath(sf.mountTimeout, sf.umountTimeout, path, server, serverpath, "nfs", nfsMountFunc, nfsUmountFunc)
					if err != nil {
						return []string{err.Error()}
					}
					// 3. check the nfs path using timeout command in case of nfs down 
					err, symlink := checkSymlinkUsingCmd(checkPath, sf.mountTimeout)
					if err != nil {
						errList = append(errList, err.Error())
					} else if symlink {
						errList = append(errList, fmt.Sprintf("[nfs] %v:%v is a symlink which is not allowed.", server, path))
					}
				}
			}
			if !found {
				return []string{fmt.Sprintf("not found match path for '%v' at nfs server '%v'", path, server)}
			}
		} else if vol.VolumeSource.HostPath != nil {
			// if hostpath is not found, kubelet will create directory/file according hostpath type, so just skip
			// NOTICE: if vol.HostPath.Path is not found, checkSymlinkUsingOSFunc will return [nil, false]
			// if hostpath is a symlink or got a error, add it to errlist
			err, symlink := checkSymlinkUsingOSFunc(vol.HostPath.Path)
			if err != nil {
				errList = append(errList, err.Error())
			} else if symlink {
				errList = append(errList, fmt.Sprintf("[hostpath] %v is a symlink which is not allowed.", vol.HostPath.Path))
			}
		}
	}
	return errList
}

