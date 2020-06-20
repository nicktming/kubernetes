package mountpod


import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet-tming/config"
	kubepod "k8s.io/kubernetes/pkg/kubelet-tming/pod"
	utilstrings "k8s.io/utils/strings"
)

// Manager is an interface that tracks pods with mount utilities for individual volume plugins
type Manager interface {
	GetMountPod(pluginName string) (pod *v1.Pod, container string, err error)
}


// basicManager is simple implementation of Manager. Pods with mount utilities
// are registered by placing a JSON file into
// /var/lib/kubelet/plugin-containers/<plugin name>.json and this manager just
// finds them there.
type basicManager struct {
	registrationDirectory string
	podManager            kubepod.Manager
}

// volumePluginRegistration specified format of the json files placed in
// /var/lib/kubelet/plugin-containers/
type volumePluginRegistration struct {
	PodName       string `json:"podName"`
	PodNamespace  string `json:"podNamespace"`
	PodUID        string `json:"podUID"`
	ContainerName string `json:"containerName"`
}

// NewManager returns a new mount pod manager.
func NewManager(rootDirectory string, podManager kubepod.Manager) (Manager, error) {
	regPath := path.Join(rootDirectory, config.DefaultKubeletPluginContainersDirName)

	// Create the directory on startup
	os.MkdirAll(regPath, 0700)

	return &basicManager{
		registrationDirectory: regPath,
		podManager:            podManager,
	}, nil
}

func (m *basicManager) getVolumePluginRegistrationPath(pluginName string) string {
	// sanitize plugin name so it does not escape directory
	safePluginName := utilstrings.EscapeQualifiedName(pluginName) + ".json"
	return path.Join(m.registrationDirectory, safePluginName)
}

func (m *basicManager) GetMountPod(pluginName string) (pod *v1.Pod, containerName string, err error) {
	// Read /var/lib/kubelet/plugin-containers/<plugin name>.json
	regPath := m.getVolumePluginRegistrationPath(pluginName)
	regBytes, err := ioutil.ReadFile(regPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No pod is registered for this plugin
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("cannot read %s: %v", regPath, err)
	}

	// Parse json
	var reg volumePluginRegistration
	if err := json.Unmarshal(regBytes, &reg); err != nil {
		return nil, "", fmt.Errorf("unable to parse %s: %s", regPath, err)
	}
	if len(reg.ContainerName) == 0 {
		return nil, "", fmt.Errorf("unable to parse %s: \"containerName\" is not set", regPath)
	}
	if len(reg.PodUID) == 0 {
		return nil, "", fmt.Errorf("unable to parse %s: \"podUID\" is not set", regPath)
	}
	if len(reg.PodNamespace) == 0 {
		return nil, "", fmt.Errorf("unable to parse %s: \"podNamespace\" is not set", regPath)
	}
	if len(reg.PodName) == 0 {
		return nil, "", fmt.Errorf("unable to parse %s: \"podName\" is not set", regPath)
	}

	pod, ok := m.podManager.GetPodByName(reg.PodNamespace, reg.PodName)
	if !ok {
		return nil, "", fmt.Errorf("unable to process %s: pod %s/%s not found", regPath, reg.PodNamespace, reg.PodName)
	}
	if string(pod.UID) != reg.PodUID {
		return nil, "", fmt.Errorf("unable to process %s: pod %s/%s has unexpected UID", regPath, reg.PodNamespace, reg.PodName)
	}
	// make sure that reg.ContainerName exists in the pod
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == reg.ContainerName {
			return pod, reg.ContainerName, nil
		}
	}
	return nil, "", fmt.Errorf("unable to process %s: pod %s/%s has no container named %q", regPath, reg.PodNamespace, reg.PodName, reg.ContainerName)

}
