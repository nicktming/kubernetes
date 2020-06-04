#!/usr/bin/env bash

homepath=/root/mingtingzhang
k8spath=$homepath/k8s

config=""
config="$config --kubeconfig=$homepath/ssl_keys/node/kubelet_kubeconfig"
#config="$config --network-plugin=cni"
#config="$config --cluster-domain=cluster.local"
#config="$config --cluster-dns=169.169.0.10"
#config="$config --runtime-cgroups=/systemd/system.slice --kubelet-cgroups=/systemd/system.slice"

./kubelet-tming $config


// TODO
// 1. /proc/$$/cgroup
// 2. Check whether swap is enabled. The Kubelet does not support running with swap enabled.
// 3. MemoryPressureCondition find v1.NodeMemoryPressure to break
// 4. kubelet custom metrics

