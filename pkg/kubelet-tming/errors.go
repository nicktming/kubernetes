package kubelet_tming

import "errors"

const (
	// NetworkNotReadyErrorMsg is used to describe the error that network is not ready
	NetworkNotReadyErrorMsg = "network is not ready"
)

var (
	// ErrNetworkUnknown indicates the network state is unknown
	ErrNetworkUnknown = errors.New("network state unknown")
)

