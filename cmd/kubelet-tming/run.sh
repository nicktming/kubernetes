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
// 5. 当qosContainerManagerImpl还没有启动完成, 也就是m.qosContainersInfo的信息还没有设置成功, 此时有一个pod进来读到的m.qosContainersInfo将为"", 造成延迟
// 6. a. 先删pv 导致pending 再删pvc就可以把pv删除, 但是/local-path-storage中并没有清理
//    b. 先删pvc, 会直接删pv, 也会把/local-path-storage里面的内容删除 storageclass的策略是DELETE pvc与pv一一对应

// 7. 如果先创建pv, 会怎么样?
