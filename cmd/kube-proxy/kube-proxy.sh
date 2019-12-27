#!/usr/bin/env bash

k8spath=/root/go/src/k8s.io/kubernetes/cmd

config=""
config="$config --kubeconfig=/root/ssl_keys/node/kubelet_kubeconfig"
config="$config --hostname-override=master"
config="$config --master=https://172.31.71.181:6443"

$k8spath/kube-proxy/kube-proxy $config

