#!/usr/bin/env bash

wget https://github.com/coreos/flannel/releases/download/v0.11.0/flannel-v0.11.0-linux-amd64.tar.gz

tar -zxvf flannel-v0.11.0-linux-amd64.tar.gz
cp flanneld mk-docker-opts.sh /usr/local/bin/


etcdctl --endpoints http://127.0.0.1:2379 set /coreos.com/network/config '{"Network": "10.0.0.0/16", "SubnetLen": 24, "SubnetMin": "10.0.1.0","SubnetMax": "10.0.20.0", "Backend": {"Type": "vxlan"}}'

etcdctl get /coreos.com/network/config

/usr/local/bin/flanneld --etcd-endpoints="http://172.21.0.16:2379" --ip-masq=true


cni
git clone https://github.com/containernetworking/cni.git

mkdir -p /opt/cni/bin
cp -r bin/* /opt/cni/bin



cp flanneld mk-docker-opts.sh /usr/local/bin
cp flannel.service /usr/lib/systemd/system
systemctl enable flannel
systemctl daemon-reload
sytemctl restart flannel



mkdir -p /etc/cni/net.d
cp 10-flannel.conf /etc/cni/net.d
