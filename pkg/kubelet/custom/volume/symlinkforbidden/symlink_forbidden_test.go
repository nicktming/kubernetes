package symlinkforbidden

import (
	"io/ioutil"
	"os"
	"fmt"
	"path"
	"testing"
	"time"
	"strings"
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

const defaultPerm = 0750

func TestSymlinkForbidden(t *testing.T) {
	sf, err := NewSymlinkForbidden(time.Second, time.Second)
	if err != nil {
		t.Errorf("init symlink forbidden error %v", err)
	}
	tests := []struct {
		name 					string
		prepareVol    			func(pod *v1.Pod, base string) error 
		shouldFail 				bool 
		expectedAdmit 			bool 
	}{
		{
			name: "pod without vol",
			prepareVol: func(pod *v1.Pod, base string) error {
				return nil
			},
			shouldFail: false,
			expectedAdmit: true,
		},
		{
			name: "pod with link dir vol",
			prepareVol: func(pod *v1.Pod, base string) error {
				origin := path.Join(base, "original")
				link := path.Join(base, "link")
				if err := os.Mkdir(origin, defaultPerm); err != nil {
					return err
				}

				if err := os.Symlink(origin, link); err != nil {
					return err
				}
				linkVol := v1.Volume{
					Name: "linkVol",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: link,
						},
					},
				}
				pod.Spec.Volumes = append(pod.Spec.Volumes, []v1.Volume{linkVol}...)
				return nil
			},	
			shouldFail: false,
			expectedAdmit: false,
		},
		{
			name: "pod with link file vol",
			prepareVol: func(pod *v1.Pod, base string) error {
				origin := path.Join(base, "original.txt")
				link := path.Join(base, "link.txt")

				err := ioutil.WriteFile(origin, []byte("hello world"), 0600)
				if err != nil {
					return err
				}

				if err = os.Symlink(origin, link); err != nil {
					return err
				}
				linkVol := v1.Volume{
					Name: "linkVol",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: link,
						},
					},
				}
				pod.Spec.Volumes = append(pod.Spec.Volumes, []v1.Volume{linkVol}...)
				return nil
			},	
			shouldFail: false,
			expectedAdmit: false,
		},
		{
			name: "pod with normal vol",
			prepareVol: func(pod *v1.Pod, base string) error {
				normalDir := path.Join(base, "normal")
				if err := os.Mkdir(normalDir, defaultPerm); err != nil {
					return err
				}
				normalFile := path.Join(base, "normal.txt")

				err := ioutil.WriteFile(normalFile, []byte("hello world"), 0600)
				if err != nil {
					return err
				}
				normalVol := v1.Volume {
					Name: "normalDirVol",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: normalDir,
						},
					},
				}
				normalFileVol := v1.Volume {
					Name: "normalFileVol",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: normalFile,
						},
					},
				}
				pod.Spec.Volumes = append(pod.Spec.Volumes, []v1.Volume{normalVol, normalFileVol}...)
				return nil
			},	
			shouldFail: false,
			expectedAdmit: true,
		},
		{
			name: "pod with notfound vol",
			prepareVol: func(pod *v1.Pod, base string) error {
				notfound := path.Join(base, "notfound")
				notfoundVol := v1.Volume{
					Name: "notfoundVol",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: notfound,
						},
					},
				}
				pod.Spec.Volumes = append(pod.Spec.Volumes, []v1.Volume{notfoundVol}...)
				return nil
			},	
			shouldFail: false,
			expectedAdmit: true,
		},
		{
			name: "pod with arbitrary nfs server vol",
			prepareVol: func(pod *v1.Pod, base string) error {
				arbitraryNFSVol := v1.Volume{
					Name: "arbitraryNFSVol",
					VolumeSource: v1.VolumeSource{
						NFS: &v1.NFSVolumeSource{
							Server:   "1.2.3.4",
							Path:     "/mnt/nfs/dir",
						},
					},
				}				
				pod.Spec.Volumes = append(pod.Spec.Volumes, []v1.Volume{arbitraryNFSVol}...)
				return nil
			},	
			shouldFail: false,
			expectedAdmit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &v1.Pod{}
			base, err := ioutil.TempDir("", "testsymlink")
			if err != nil {
				fmt.Printf("failed to create tmpdir: %v\n", err)
				return
			}
			defer os.RemoveAll(base)

			if tt.prepareVol == nil {
				t.Fatalf("prepareVol function required")
			}
			tt.prepareVol(pod, base)
			attrs := &lifecycle.PodAdmitAttributes {
				Pod: pod,
			}
			actual := sf.Admit(attrs)
			if tt.shouldFail && err == nil {
				t.Errorf("Expected an error in %v", tt.name)
			} else if !tt.shouldFail && err != nil {
				t.Errorf("Unexpected error in %v, got %v", tt.name, err)
			}  else if tt.expectedAdmit != actual.Admit {
				t.Errorf("Failed at case: ['%v'], Expected %v, got %v", tt.name, tt.expectedAdmit, actual.Admit)
			}
		})
	}
}

func TestSymlink(t *testing.T) {
	type returnfield struct {
		err error
		symlink bool
	}
	tests := []struct {
		name 					string
		prepare   				func(base string) ([]string, error)
		expected				[]returnfield
		symlinkFuncWithTimeout 	func(path string, timeout time.Duration) (error, bool)
		symlinkFunc 			func(path string) (error, bool)
		timeout 				time.Duration 
	}{
		{
			name: "check symlink with files",
			prepare: func(base string) ([]string, error) {
				origin := path.Join(base, "original.txt")
				link := path.Join(base, "link.txt")
				notfound := path.Join(base, "notfound")

				err := ioutil.WriteFile(origin, []byte("hello world"), 0600)
				if err != nil {
					return nil, err
				}

				if err = os.Symlink(origin, link); err != nil {
					return nil, err
				}
				return []string{origin, link, notfound}, nil
			},
			expected: []returnfield {
				{nil, false},
				{nil, true},
				{nil, false},
			},
			symlinkFunc: checkSymlinkUsingOSFunc,
		},
		{
			name: "check symlink with directories",
			prepare: func(base string) ([]string, error) {
				origin := path.Join(base, "original")
				link := path.Join(base, "link")
				notfound := path.Join(base, "notfound")

				if err := os.Mkdir(origin, defaultPerm); err != nil {
					return nil, err
				}

				if err := os.Symlink(origin, link); err != nil {
					return nil, err
				}
				return []string{origin, link, notfound}, nil
			},
			expected: []returnfield {
				{nil, false},
				{nil, true},
				{nil, false},
			},
			symlinkFunc: checkSymlinkUsingOSFunc,
		},
		{
			name: "check symlink with files using cmd",
			prepare: func(base string) ([]string, error) {
				origin := path.Join(base, "original.txt")
				link := path.Join(base, "link.txt")
				notfound := path.Join(base, "notfound")

				err := ioutil.WriteFile(origin, []byte("hello world"), 0600)
				if err != nil {
					return nil, err
				}

				if err = os.Symlink(origin, link); err != nil {
					return nil, err
				}
				return []string{origin, link, notfound}, nil
			},
			expected: []returnfield {
				{nil, false},
				{nil, true},
				{nil, false},
			},
			symlinkFuncWithTimeout: checkSymlinkUsingCmd,
			timeout: 	time.Second,
		},
		{
			name: "check symlink with directories using cmd",
			prepare: func(base string) ([]string, error) {
				origin := path.Join(base, "original")
				link := path.Join(base, "link")
				notfound := path.Join(base, "notfound")

				if err := os.Mkdir(origin, defaultPerm); err != nil {
					return nil, err
				}

				if err := os.Symlink(origin, link); err != nil {
					return nil, err
				}
				return []string{origin, link, notfound}, nil
			},
			expected: []returnfield {
				{nil, false},
				{nil, true},
				{nil, false},
			},
			symlinkFuncWithTimeout: checkSymlinkUsingCmd,
			timeout: 	time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, err := ioutil.TempDir("", "testsymlink")
			if err != nil {
				fmt.Printf("failed to create tmpdir: %v\n", err)
				return
			}
			defer os.RemoveAll(base)

			if tt.prepare == nil {
				t.Fatalf("prepare function required")
			}

			paths, err := tt.prepare(base)
			if err != nil {
				t.Fatalf("failed to prepare test: %v", err)
			}
			var symlink bool 
			for i, path := range paths {
				expected := tt.expected[i]
				
				if tt.symlinkFuncWithTimeout != nil {
					err, symlink = tt.symlinkFuncWithTimeout(path, tt.timeout)
				} else {
					err, symlink = tt.symlinkFunc(path)
				}
				if err != expected.err || symlink != expected.symlink {
					t.Fatalf("got: %v, Expected: %v", returnfield{err, symlink}, expected)
				}
			}
		})
	}
}


func TestPrepareNFSCheckPath(t *testing.T) {
	mountFunc := func(source, dst, fstype string, timeout time.Duration) error {
		return nil 
	}
	umountFunc := func(dst string, timeout time.Duration) {
		return 
	}
	mountTimeout, umountTimeout, fstype, server := time.Second, time.Second, "nfs", "0.0.0.0"

	check := func(name, actual, mountpoint, originPath, serverPath string) {
		subdir := strings.TrimLeft(originPath, serverPath)
		expected := path.Join(mountpoint, subdir)
		if actual != expected {
			t.Fatalf("Failed at ['%v'], got: %v, want: %v", name, actual, expected)
		}
	}

	tests := []struct {
		name 					string
		originPath    			string 
		serverPath 				string
		mount 					func(source, dst, fstype string, timeout time.Duration) error
		shouldFail 				bool 
	}{
		{
			name: 	"test case 1",
			originPath: "/mnt/nfs/zhangsan/test",
			serverPath: "/mnt/nfs",
			mount: mountFunc,
			shouldFail: false,
		},
		{
			name: 	"test case 2",
			originPath: "/mnt/nfs/zhangsan/test/mnt/nfs",
			serverPath: "/mnt/nfs",
			mount: mountFunc,
			shouldFail: false,
		},
		{
			name: 			"test case 3 with mount func error",
			originPath: 	"/mnt/nfs/zhangsan/test/mnt/nfs",
			serverPath: 	"/mnt/nfs",
			mount: 			func(source, dst, fstype string, timeout time.Duration) error{
								return fmt.Errorf("timeout")
							},
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkPath, mountpoint, err := prepareNFSCheckPath(mountTimeout, umountTimeout, tt.originPath, server, tt.serverPath, fstype, tt.mount, umountFunc)
			if tt.shouldFail && err == nil {
				t.Errorf("Expected an error in %v", tt.name)
			} else if !tt.shouldFail && err != nil {
				t.Errorf("Unexpected error in %v, got %v", tt.name, err)
			} 
			if tt.shouldFail {
				return 
			}
			check(tt.name, checkPath, mountpoint, tt.originPath, tt.serverPath)	
		})
	}
}
