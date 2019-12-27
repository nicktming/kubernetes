#!/usr/bin/env bash

k8spath=/root/go/src/k8s.io/kubernetes/cmd

config=""
config="$config --kubeconfig=/root/ssl_keys/node/kubelet_kubeconfig"
config="$config --hostname-override=master"
config="$config --network-plugin=cni"

$k8spath/kube-proxy/kube-proxy $config

