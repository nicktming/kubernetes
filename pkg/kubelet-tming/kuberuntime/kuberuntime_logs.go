package kuberuntime

import (
	"context"
	"io"
	"time"

	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/kuberuntime/logs"
)

// ReadLogs read the container log and redirect into stdout and stderr.
// Note that containerID is only needed when following the log, or else
// just pass in empty string "".
func (m *kubeGenericRuntimeManager) ReadLogs(ctx context.Context, path, containerID string, apiOpts *v1.PodLogOptions, stdout, stderr io.Writer) error {
	// Convert v1.PodLogOptions into internal log options.
	opts := logs.NewLogOptions(apiOpts, time.Now())

	return logs.ReadLogs(ctx, path, containerID, opts, m.runtimeService, stdout, stderr)
}
