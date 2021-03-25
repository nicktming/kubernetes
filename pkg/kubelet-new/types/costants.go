package types

const (
	// system default DNS resolver configuration
	ResolvConfDefault = "/etc/resolv.conf"

	// different container runtimes
	DockerContainerRuntime = "docker"
	RemoteContainerRuntime = "remote"

	// User visible keys for managing node allocatable enforcement on the node.
	NodeAllocatableEnforcementKey = "pods"
	SystemReservedEnforcementKey  = "system-reserved"
	KubeReservedEnforcementKey    = "kube-reserved"
	NodeAllocatableNoneKey        = "none"
)
