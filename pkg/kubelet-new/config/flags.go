package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/spf13/pflag"
	"fmt"
)


type ContainerRuntimeOptions struct {
	// General options

	ContainerRuntime string


	RuntimeCgroups string

	RedirectContainerStreaming bool

	DockershimRootDirectory string

	ExperimentalDockershim bool

	PodSandboxImage string


	DockerEndpoint string

	ImagePullProgressDeadline metav1.Duration

	NetworkPluginName string

	NetworkPluginMTU int32

	CNIConfDir string

	CNIBinDir string
}


func (s *ContainerRuntimeOptions) AddFlags(fs *pflag.FlagSet) {
	dockerOnlyWarning := "This docker-specific flag only works when container-runtime is set to docker."

	// General settings.
	fs.StringVar(&s.ContainerRuntime, "container-runtime", s.ContainerRuntime, "The container runtime to use. Possible values: 'docker', 'remote', 'rkt (deprecated)'.")
	fs.StringVar(&s.RuntimeCgroups, "runtime-cgroups", s.RuntimeCgroups, "Optional absolute name of cgroups to create and run the runtime in.")
	fs.BoolVar(&s.RedirectContainerStreaming, "redirect-container-streaming", s.RedirectContainerStreaming, "Enables container streaming redirect. If false, kubelet will proxy container streaming data between apiserver and container runtime; if true, kubelet will return an http redirect to apiserver, and apiserver will access container runtime directly. The proxy approach is more secure, but introduces some overhead. The redirect approach is more performant, but less secure because the connection between apiserver and container runtime may not be authenticated.")

	// Docker-specific settings.
	fs.BoolVar(&s.ExperimentalDockershim, "experimental-dockershim", s.ExperimentalDockershim, "Enable dockershim only mode. In this mode, kubelet will only start dockershim without any other functionalities. This flag only serves test purpose, please do not use it unless you are conscious of what you are doing. [default=false]")
	fs.MarkHidden("experimental-dockershim")
	fs.StringVar(&s.DockershimRootDirectory, "experimental-dockershim-root-directory", s.DockershimRootDirectory, "Path to the dockershim root directory.")
	fs.MarkHidden("experimental-dockershim-root-directory")
	fs.StringVar(&s.PodSandboxImage, "pod-infra-container-image", s.PodSandboxImage, fmt.Sprintf("The image whose network/ipc namespaces containers in each pod will use. %s", dockerOnlyWarning))
	fs.StringVar(&s.DockerEndpoint, "docker-endpoint", s.DockerEndpoint, fmt.Sprintf("Use this for the docker endpoint to communicate with. %s", dockerOnlyWarning))
	fs.DurationVar(&s.ImagePullProgressDeadline.Duration, "image-pull-progress-deadline", s.ImagePullProgressDeadline.Duration, fmt.Sprintf("If no pulling progress is made before this deadline, the image pulling will be cancelled. %s", dockerOnlyWarning))

	// Network plugin settings for Docker.
	fs.StringVar(&s.NetworkPluginName, "network-plugin", s.NetworkPluginName, fmt.Sprintf("<Warning: Alpha feature> The name of the network plugin to be invoked for various events in kubelet/pod lifecycle. %s", dockerOnlyWarning))
	fs.StringVar(&s.CNIConfDir, "cni-conf-dir", s.CNIConfDir, fmt.Sprintf("<Warning: Alpha feature> The full path of the directory in which to search for CNI config files. %s", dockerOnlyWarning))
	fs.StringVar(&s.CNIBinDir, "cni-bin-dir", s.CNIBinDir, fmt.Sprintf("<Warning: Alpha feature> A comma-separated list of full paths of directories in which to search for CNI plugin binaries. %s", dockerOnlyWarning))
	fs.Int32Var(&s.NetworkPluginMTU, "network-plugin-mtu", s.NetworkPluginMTU, fmt.Sprintf("<Warning: Alpha feature> The MTU to be passed to the network plugin, to override the default. Set to 0 to use the default 1460 MTU. %s", dockerOnlyWarning))
}
