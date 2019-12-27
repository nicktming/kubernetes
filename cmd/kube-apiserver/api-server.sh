#!/usr/bin/env bash

k8spath=/root/go/src/k8s.io/kubernetes/cmd

config=""
config="$config --client-ca-file=/root/ssl_keys/ca.crt"
config="$config --tls-private-key-file=/root/ssl_keys/server.key"
config="$config --tls-cert-file=/root/ssl_keys/server.crt"
config="$config --storage-backend=etcd3"
config="$config --etcd-servers=http://127.0.0.1:2379"
config="$config --insecure-bind-address=0.0.0.0"
config="$config --insecure-port=8080"
config="$config --secure-port=6443"
config="$config --service-cluster-ip-range=169.169.0.0/16"
config="$config --service-node-port-range=100-30000"
config="$config --admission-control=NamespaceLifecycle,LimitRanger,ServiceAccount,DefaultStorageClass,ResourceQuota,MutatingAdmissionWebhook,ValidatingAdmissionWebhook"

$k8spath/kube-apiserver/kube-apiserver $config

