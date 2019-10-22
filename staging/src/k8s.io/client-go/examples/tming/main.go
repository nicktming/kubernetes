package main

import (
	"fmt"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"time"
)

func main()  {
	config := &rest.Config{
		Host: "http://172.21.0.16:8080",
	}
	client := clientset.NewForConfigOrDie(config)
	// 生成一个SharedInformerFactory
	factory := informers.NewSharedInformerFactory(client, 5 * time.Second)
	// 生成一个PodInformer
	podInformer := factory.Core().V1().Pods()
	// 获得一个cache.SharedIndexInformer 单例模式
	sharedInformer := podInformer.Informer()

	sharedInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) {fmt.Printf("add: %v\n", obj.(*v1.Pod).Name)},
		UpdateFunc: func(oldObj, newObj interface{}) {fmt.Printf("update: %v\n", newObj.(*v1.Pod).Name)},
		DeleteFunc: func(obj interface{}){fmt.Printf("delete: %v\n", obj.(*v1.Pod).Name)},
	})

	stopCh := make(chan struct{})

	// 第一种方式
	// 可以这样启动  也可以按照下面的方式启动
	// go sharedInformer.Run(stopCh)
	// time.Sleep(2 * time.Second)

	// 第二种方式
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)

	pods, _ := podInformer.Lister().Pods("default").List(labels.Everything())

	for _, p := range pods {
		fmt.Printf("list pods: %v\n", p.Name)
	}
	<- stopCh
}
