#!/usr/bin/env bash

openssl genrsa -out ca.key 2048
openssl req -x509 -new -nodes -key ca.key -subj "/CN=master" -days 5000 -out ca.crt
openssl genrsa -out server.key 2048

master_ssl.cnf
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
[req_distinguished_name]
[v3_req]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
subjectAltName = @alt_names
[alt_names]
DNS.1 = kubernetes
DNS.2 = kubernetes.default
DNS.3 = kubernetes.default.svc
DNS.4 = kubernetes.default.svc.cluster.loca
DNS.5 = master
IP.1 = 169.169.0.1
IP.2 = 172.31.71.181


openssl req -new -key server.key -subj "/CN=master" -config master_ssl.cnf -out server.csr
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 5000 -extensions v3_req -extfile master_ssl.cnf -out server.crt

kubectl-controller-manager
openssl genrsa -out cs_client.key 2048
openssl req -new -key cs_client.key -subj "/CN=master" -out cs_client.csr
openssl x509 -req -in cs_client.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out cs_client.crt -days 5000

/etc/kubernetes/kubeconfig kube-controller-manager/kube-scheduler

apiVersion: v1
kind: Config
users:
- name: controllermanager
  user:
    client-certificate: /root/ssl_keys/cs_client.crt
    client-key: /root/ssl_keys/cs_client.key
clusters:
- name: local
  cluster:
    certificate-authority: /root/ssl_keys/ca.crt
contexts:
- context:
    cluster: local
    user: controllermanager
  name: my-context
current-context: my-context



[kubelet/kube-proxy]
openssl genrsa -out kubelet_client.key 2048
openssl req -new -key kubelet_client.key -subj "/CN=172.31.71.181" -out kubelet_client.csr
openssl x509 -req -in kubelet_client.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out kubelet_client.crt -days 5000

apiVersion: v1
kind: Config
users:
- name: kubelet
  user:
    client-certificate: /root/ssl_keys/node/kubelet_client.crt
    client-key: /root/ssl_keys/node/kubelet_client.key
clusters:
- name: local
  cluster:
    certificate-authority: /root/ssl_keys/node/ca.crt
contexts:
- context:
    cluster: local
    user: kubelet
  name: my-context
current-context: my-context
