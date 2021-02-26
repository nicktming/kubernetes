package symlinkforbidden

import (
	"os"
	"fmt"
	"k8s.io/klog"
	"strings"
	"os/exec"
	"syscall"
	"time"
	"bytes"
	"io/ioutil"
)

/**
example: 
# showmount -e ${ip}
Export list for ${ip}:
/mnt/nfs *

findNFSServerPaths will return []string{"Export", "/mnt/nfs"}
*/
func findNFSServerPaths(server string) ([]string, error) {
	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("showmount -e %s | awk '{print $1}'", server))
	output, err, _ := commandOutputWithTimeout(cmd, time.Second, nil, nil)
	if err != nil {
		return nil, err
	}
	// if nfs is down, command output will be: 'clnt_create: RPC: Program not registered'
	// if rpc is down, command output will be: 'clnt_create: RPC: Port mapper failure - Unable to receive: errno 111 (Connection refused)'
	if strings.Contains(string(output), nfsErr) || strings.Contains(string(output), rpcErr) {
		return nil, fmt.Errorf("client can not connect nfs server (%v)", server)
	}
	serverpaths := strings.Split(string(output), "\n")
	return serverpaths, nil
}

func umountWithTimeout(dst string, timeout time.Duration) {
	cmd := exec.Command("umount", "-f", dst)
	// if nfs is down, let the umount process at system to finish umount when nfs is recover
	cleanupDir := func() error {
		return os.Remove(dst)
	}
	_, err, _ := commandOutputWithTimeout(cmd, timeout, cleanupDir, nil)
	if err != nil {
		klog.Warningf(err.Error())
	}
}

func mountWithTimeout(source, dst, fstype string, timeout time.Duration) error {
	if isMounted(source, dst, timeout) == nil {
		klog.V(4).Infof("%v -> %v already mounted\n", source, dst)
		return nil
	}
	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("mount -t %v %v %v", fstype, source, dst))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err, _ := commandOutputWithTimeout(cmd, timeout, nil, killMountProcessTimeout)
	if err != nil {
		return err
	}
	if output == nil {
		return fmt.Errorf("command %v output is nil", cmd.Args)
	}
	return isMounted(source, dst, timeout)
}

func findMountInfos(timeout time.Duration) (map[string]string, error) {
	cmd := exec.Command("/bin/sh", "-c", "awk '{print $1,$2}' /proc/mounts")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	mountsinfo, err, _ := commandOutputWithTimeout(cmd, timeout, nil, killMountProcessTimeout)
	if err != nil {
		return nil, err
	}
	if mountsinfo == nil {
		return nil, fmt.Errorf("command %v output is nil", cmd.Args)
	}
	mounts := strings.Split(string(mountsinfo), "\n")

	mountinfos := make(map[string]string)
	for _, mount := range mounts {
		sourceAndDst := strings.Split(string(mount), " ")
		if len(sourceAndDst) == 2 {
			mountinfos[sourceAndDst[1]] = sourceAndDst[0]
		}
	}
	return mountinfos, nil
}

// check whether is mounted
// 1. mounted, return nil
// 2. not mounted or got err, return err 
func isMounted(source, dst string, timeout time.Duration) error {
	mountinfos, err := findMountInfos(timeout)
	if err != nil {
		return err
	}
	src, ok := mountinfos[dst]
	if !ok {
		return fmt.Errorf("%v is not mounted to %v", source, dst)
	}
	if source == "" {
		return nil
	}
	if src != source {
		return fmt.Errorf("%v is not mounted to %v", source, dst)
	}
	return nil
}

// the function to deal with cmd when command executes timeout
type timeoutFunc func(cmd *exec.Cmd) error
// the function to do somthing (such as cleanup) after command executes successfully
type postExitFunc func() error

var killMountProcessTimeout = func(cmd *exec.Cmd) error {
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return err
	}
	// kill the mount process group when timeout, mostly becase of nfs server connection
	if err := syscall.Kill(-pgid, 15); err != nil {
		return err
	}
	return fmt.Errorf("mount nfs timeout and process %v got killed", pgid)
}

func commandOutputWithTimeout(cmd *exec.Cmd, timeout time.Duration, pef postExitFunc, tf timeoutFunc) ([]byte, error, bool)  {
	var b bytes.Buffer
	cmd.Stdout = &b
	cmd.Stderr = &b
	var err error
	if err := cmd.Start(); err != nil {
		return b.Bytes(), err, false
	}
	done := make(chan error, 1)
	stop := make(chan struct{}, 1)
	go func() {
		select {
		case done <- cmd.Wait():
			return
		case <- stop:
			return
		}
	}()

	select {
	case err = <-done:
		if err != nil {
			return b.Bytes(), err, false
		}
		if pef != nil {
			err = pef()
		}
		return b.Bytes(), err, false
	case <-time.After(timeout):
		close(stop)
		if tf != nil {
			return nil, tf(cmd), true
		}
		return nil, fmt.Errorf("timeout for cmd: %v", cmd), true
	}
}

// os.lstat will hang if nfs is down, so using cmd with timout in case of nfs problem.
// since nfs is down, 'file' command process will be killed

// if nfs is down, the file process will be killed and return [err, false]
// if path is symlink, return [nil, true]
// if path is not a symlink or not found, return [nil, false]
func checkSymlinkUsingCmd(path string, timeout time.Duration) (error, bool) {
	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("file %v", path))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err, isTimeout := commandOutputWithTimeout(cmd, timeout, nil, killMountProcessTimeout)
	if err != nil {
		return err, false
	}
	if isTimeout {
		return fmt.Errorf("timeout: nfs may be not connected"), false
	}
	// this should not happen normally
	if output == nil {
		return fmt.Errorf("command %v output is nil", cmd.Args), false
	}
	if strings.Contains(string(output), "symbolic link to") {
		return nil, true
	}
	// if the dir/file not foud, return nil, false 
	return nil, false
}

// error symlink
func checkSymlinkUsingOSFunc(path string) (error, bool) {
	klog.V(4).Infof("[symlink forbidden] check path isSymlink: %v", path)
	fileInfo, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false
		}
		return err, false
	}
	if (fileInfo.Mode() & os.ModeSymlink) != 0 {
		return nil, true
	}
	return err, false
}

func prepareNFSCheckPath(mountTimeout, umountTimeout time.Duration, 
		originpath, server, serverpath, fstype string,
		mount mountFunc, umount umountFunc) (string, string, error) {

	source := fmt.Sprintf("%s:%s", server, serverpath)
	dst, err := ioutil.TempDir(basepath, "nfs")
	if err != nil {
		return "", "", err 
	}
	if err := mount(source, dst, fstype, mountTimeout); err != nil {
		return "", "", err 
	}
	defer func() {
		klog.V(4).Infof("[symlink forbidden] umount for %v", dst)
		umount(dst, umountTimeout)
	}()
	checkPath := strings.Replace(originpath, serverpath, dst, 1)
	return checkPath, dst, nil 
}

// if there still has mounted dir, do umount. but it happens with a very small probability.
// ioutil.ReadDir(basepath) will hang if nfs down, using timeout command instead.
func cleanup(timeout time.Duration, umount umountFunc) {
	mountsinfo, err := findMountInfos(timeout)
	if err != nil {
		klog.Warningf("[symlink forbidden] find mountinfos err %v", err)
		return
	}
	for dst := range mountsinfo {
		if strings.HasPrefix(dst, basepath) {
			klog.V(4).Infof("[symlink forbidden] clean up unused mountpoint '%v'\n", dst)
			umount(dst, timeout)
		}
	}
}

