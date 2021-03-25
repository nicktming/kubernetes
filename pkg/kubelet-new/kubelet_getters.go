package kubelet_new

import (
	"path/filepath"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet-new/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet-new/container"
	"k8s.io/kubernetes/pkg/util/mount"
	utilpath "k8s.io/utils/path"
	"fmt"
	"k8s.io/klog"
	"io/ioutil"
	cadvisorapiv1 "github.com/google/cadvisor/info/v1"
)

func (kl *Kubelet) getRootDir() string {
	return kl.rootDirectory
}

func (kl *Kubelet) getPodsDir() string {
	return filepath.Join(kl.getRootDir(), config.DefaultKubeletPodsDirName)
}

// getPodContainerDir returns the full path to the per-pod data directory under
// which container data is held for the specified pod.  This directory may not
// exist if the pod or container does not exist.
func (kl *Kubelet) getPodContainerDir(podUID types.UID, ctrName string) string {
	return filepath.Join(kl.getPodDir(podUID), config.DefaultKubeletContainersDirName, ctrName)
}

// getPodDir returns the full path to the per-pod directory for the pod with
// the given UID.
func (kl *Kubelet) getPodDir(podUID types.UID) string {
	return filepath.Join(kl.getPodsDir(), string(podUID))
}

// getPodPluginsDir returns the full path to the per-pod data directory under
// which plugins may store data for the specified pod.  This directory may not
// exist if the pod does not exist.
func (kl *Kubelet) getPodPluginsDir(podUID types.UID) string {
	return filepath.Join(kl.getPodDir(podUID), config.DefaultKubeletPluginsDirName)
}


// getPluginsDir returns the full path to the directory under which plugin
// directories are created.  Plugins can use these directories for data that
// they need to persist.  Plugins should create subdirectories under this named
// after their own names.
func (kl *Kubelet) getPluginsDir() string {
	return filepath.Join(kl.getRootDir(), config.DefaultKubeletPluginsDirName)
}

// getPodVolumesDir returns the full path to the per-pod data directory under
// which volumes are created for the specified pod.  This directory may not
// exist if the pod does not exist.
func (kl *Kubelet) getPodVolumesDir(podUID types.UID) string {
	return filepath.Join(kl.getPodDir(podUID), config.DefaultKubeletVolumesDirName)
}


// getPluginsRegistrationDir returns the full path to the directory under which
// plugins socket should be placed to be registered.
// More information is available about plugin registration in the pluginwatcher
// module
func (kl *Kubelet) getPluginsRegistrationDir() string {
	return filepath.Join(kl.getRootDir(), config.DefaultKubeletPluginsRegistrationDirName)
}

// getPodResourcesSocket returns the full path to the directory containing the pod resources socket
func (kl *Kubelet) getPodResourcesDir() string {
	return filepath.Join(kl.getRootDir(), config.DefaultKubeletPodResourcesDirName)
}

// getRuntime returns the current Runtime implementation in use by the kubelet.
func (kl *Kubelet) getRuntime() kubecontainer.Runtime {
	return kl.containerRuntime
}

func (kl *Kubelet) getPodVolumePathListFromDisk(podUID types.UID) ([]string, error) {
	volumes := []string{}
	podVolDir := kl.getPodVolumesDir(podUID)

	if pathExists, pathErr := mount.PathExists(podVolDir); pathErr != nil {
		return volumes, fmt.Errorf("Error checking if path %q exists: %v", podVolDir, pathErr)
	} else if !pathExists {
		klog.Warningf("Path %q does not exist", podVolDir)
		return volumes, nil
	}

	volumePluginDirs, err := ioutil.ReadDir(podVolDir)
	if err != nil {
		klog.Errorf("Could not read directory %s: %v", podVolDir, err)
		return volumes, err
	}
	for _, volumePluginDir := range volumePluginDirs {
		volumePluginName := volumePluginDir.Name()
		volumePluginPath := filepath.Join(podVolDir, volumePluginName)
		volumeDirs, err := utilpath.ReadDirNoStat(volumePluginPath)
		if err != nil {
			return volumes, fmt.Errorf("Could not read directory %s: %v", volumePluginPath, err)
		}
		for _, volumeDir := range volumeDirs {
			volumes = append(volumes, filepath.Join(volumePluginPath, volumeDir))
		}
	}
	return volumes, nil
}

// GetCachedMachineInfo assumes that the machine info can't change without a reboot
func (kl *Kubelet) GetCachedMachineInfo() (*cadvisorapiv1.MachineInfo, error) {
	return kl.machineInfo, nil
}























































