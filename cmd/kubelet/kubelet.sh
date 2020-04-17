#!/usr/bin/env bash

k8spath=/root/go/src/k8s.io/kubernetes/cmd

config=""
config="$config --kubeconfig=/root/ssl_keys/node/kubelet_kubeconfig"
config="$config --hostname-override=master"
config="$config --network-plugin=cni"
config="$config --cluster-domain=cluster.local"
config="$config --cluster-dns=169.169.0.10"
config="$config --runtime-cgroups=/systemd/system.slice --kubelet-cgroups=/systemd/system.slice"

$k8spath/kubelet/kubelet $config

