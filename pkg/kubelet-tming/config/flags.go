package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ContainerRuntimeOptions struct {

	// General options.

	// ContainerRuntime is the container runtime to use.
	ContainerRuntime string
	// RuntimeCgroups that container runtime is expected to be isolated in.
	RuntimeCgroups string
	// RedirectContainerStreaming enables container streaming redirect.
	// When RedirectContainerStreaming is false, kubelet will proxy container streaming data
	// between apiserver and container runtime. This approach is more secure, but the proxy
	// introduces some overhead.
	// When RedirectContainerStreaming is true, kubelet will return an http redirect to apiserver,
	// and apiserver will access container runtime directly. This approach is more performant,
	// but less secure because the connection between apiserver and container runtime is not
	// authenticated.
	RedirectContainerStreaming bool

	// Docker-specific options.

	// DockershimRootDirectory is the path to the dockershim root directory. Defaults to
	// /var/lib/dockershim if unset. Exposed for integration testing (e.g. in OpenShift).
	DockershimRootDirectory string
	// Enable dockershim only mode.
	ExperimentalDockershim bool
	// PodSandboxImage is the image whose network/ipc namespaces
	// containers in each pod will use.
	PodSandboxImage string
	// DockerEndpoint is the path to the docker endpoint to communicate with.
	DockerEndpoint string
	// If no pulling progress is made before the deadline imagePullProgressDeadline,
	// the image pulling will be cancelled. Defaults to 1m0s.
	// +optional
	ImagePullProgressDeadline metav1.Duration

	// Network plugin options.

	// networkPluginName is the name of the network plugin to be invoked for
	// various events in kubelet/pod lifecycle
	NetworkPluginName string
	// NetworkPluginMTU is the MTU to be passed to the network plugin,
	// and overrides the default MTU for cases where it cannot be automatically
	// computed (such as IPSEC).
	NetworkPluginMTU int32
	// CNIConfDir is the full path of the directory in which to search for
	// CNI config files
	CNIConfDir string
	// CNIBinDir is the full path of the directory in which to search for
	// CNI plugin binaries
	CNIBinDir string
}