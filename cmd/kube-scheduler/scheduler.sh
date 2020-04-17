#!/usr/bin/env bash

k8spath=/root/go/src/k8s.io/kubernetes/cmd

config=""
config="$config --master=https://172.31.241.119:6443"
config="$config --kubeconfig=/root/ssl_keys/controller_kubeconfig"

$k8spath/kube-scheduler/kube-scheduler $config

