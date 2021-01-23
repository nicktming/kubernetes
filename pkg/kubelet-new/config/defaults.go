package config

const (
	DefaultKubeletPodsDirName                = "pods"
	DefaultKubeletVolumesDirName             = "volumes"
	DefaultKubeletVolumeSubpathsDirName      = "volume-subpaths"
	DefaultKubeletVolumeDevicesDirName       = "volumeDevices"
	DefaultKubeletPluginsDirName             = "plugins"
	DefaultKubeletPluginsRegistrationDirName = "plugins_registry"
	DefaultKubeletContainersDirName          = "containers"
	DefaultKubeletPluginContainersDirName    = "plugin-containers"
	DefaultKubeletPodResourcesDirName        = "pod-resources"
	KubeletPluginsDirSELinuxLabel            = "system_u:object_r:container_file_t:s0"
)
