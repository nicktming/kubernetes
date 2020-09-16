package main

import (
	"fmt"
	cadvisorapiv2 "github.com/google/cadvisor/info/v2"
	"encoding/json"
	"k8s.io/kubernetes/pkg/kubelet/cadvisor"
)

func main() {
	containerRuntime := "docker"
	remoteRuntimeEndpoint := "unix:///var/run/dockershim.sock"
	rootDirectory := "/var/lib/kubelet"
	cgroupRoots := []string{"/kubepods", "/user.slice/user-0.slice/session-462897.scope", "/system.slice/docker.service"}
	useLegacy := true
	imageFsInfoProvider := cadvisor.NewImageFsInfoProvider(containerRuntime, remoteRuntimeEndpoint)
	client, err := cadvisor.New(imageFsInfoProvider, rootDirectory, cgroupRoots, useLegacy)
	if err != nil {
		panic(err)
	}
	maps, err := getCadvisorContainerInfo(client)
	if err != nil {
		panic(err)
	}
	for k, v := range maps {
		fmt.Printf("======key: %v=======\n", k)
		pretty_v, _ := json.MarshalIndent(v, "", "\t")
		fmt.Printf("value: %v\n", string(pretty_v))
		fmt.Println("====================")
	}

}


func getCadvisorContainerInfo(ca cadvisor.Interface) (map[string]cadvisorapiv2.ContainerInfo, error) {
	infos, err := ca.ContainerInfoV2("/", cadvisorapiv2.RequestOptions{
		IdType:    cadvisorapiv2.TypeName,
		Count:     2, // 2 samples are needed to compute "instantaneous" CPU
		Recursive: true,
	})
	if err != nil {
		if _, ok := infos["/"]; ok {
			// If the failure is partial, log it and return a best-effort
			// response.
			fmt.Errorf("Partial failure issuing cadvisor.ContainerInfoV2: %v", err)
		} else {
			return nil, fmt.Errorf("failed to get root cgroup stats: %v", err)
		}
	}
	return infos, nil
}
