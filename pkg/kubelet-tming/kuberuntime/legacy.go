package kuberuntime

import (
	"fmt"
	"path"

	kubecontainer "k8s.io/kubernetes/pkg/kubelet-tming/container"
)

// This file implements the functions that are needed for backward
// compatibility. Therefore, it imports various kubernetes packages
// directly.

const (
	// legacyContainerLogsDir is the legacy location of container logs. It is the same with
	// kubelet.containerLogsDir.
	legacyContainerLogsDir = "/var/log/containers"
	// legacyLogSuffix is the legacy log suffix.
	legacyLogSuffix = "log"

	ext4MaxFileNameLen = 255
)

// legacyLogSymlink composes the legacy container log path. It is only used for legacy cluster
// logging support.
func legacyLogSymlink(containerID string, containerName, podName, podNamespace string) string {
	return logSymlink(legacyContainerLogsDir, kubecontainer.BuildPodFullName(podName, podNamespace),
		containerName, containerID)
}

func logSymlink(containerLogsDir, podFullName, containerName, dockerID string) string {
	suffix := fmt.Sprintf(".%s", legacyLogSuffix)
	logPath := fmt.Sprintf("%s_%s-%s", podFullName, containerName, dockerID)
	// Length of a filename cannot exceed 255 characters in ext4 on Linux.
	if len(logPath) > ext4MaxFileNameLen-len(suffix) {
		logPath = logPath[:ext4MaxFileNameLen-len(suffix)]
	}
	return path.Join(containerLogsDir, logPath+suffix)
}
