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
// 8. yum install docker 整个运行机制?
// 9. pod 状态?
// 10. kubectl.kubernetes.io/last-applied-configuration https://www.codercto.com/a/24836.html
// 11. --grace-period=0
// 12. parent cgroup / child cgroup 关系
// 13. hugepage
// 14. PodDisruptionBudget
// 15. dswp.podStatusProvider.GetPodStatus(pod.UID)
// 16. Propagation
// 17. fsGroup = volumeToMount.Pod.Spec.SecurityContext.FSGroup (operation_generator.go)
// 18. BlockVolume
// 19. configmap热更新
// 20. Kubelet should mark VolumeInUse before checking if it is Attached #28095 (https://github.com/kubernetes/kubernetes/pull/28095)
// 21. 为什么要设置swap为false

