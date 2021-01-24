package kuberuntime

import (
	"strings"
	"path/filepath"
	"k8s.io/apimachinery/pkg/types"
)

// logPathDelimiter is the delimiter used in the log path.
const logPathDelimiter = "_"

// BuildPodLogsDirectory builds absolute log directory path for a pod sandbox.
func BuildPodLogsDirectory(podNamespace, podName string, podUID types.UID) string {
	return filepath.Join(podLogsRootDirectory, strings.Join([]string{podNamespace, podName,
		string(podUID)}, logPathDelimiter))
}
