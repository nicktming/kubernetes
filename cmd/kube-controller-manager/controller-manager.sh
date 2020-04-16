#!/usr/bin/env bash

k8spath=/root/go/src/k8s.io/kubernetes/cmd

config=""
config="$config --master=https://172.31.71.181:6443"
config="$config --service-account-private-key-file=/root/ssl_keys/server.key"
config="$config --root-ca-file=/root/ssl_keys/ca.crt"
config="$config --kubeconfig=/root/ssl_keys/controller_kubeconfig"
config="$config --cluster-signing-cert-file=/root/ssl_keys/ca.crt"
config="$config --cluster-signing-key-file=/root/ssl_keys/ca.key"

$k8spath/kube-controller-manager/kube-controller-manager $config

