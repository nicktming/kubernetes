package remote

import (
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"time"
	internalapi "k8s.io/cri-api/pkg/apis"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/util"
	"context"
	"google.golang.org/grpc"
)

type RemoteRuntimeService struct {
	timeout 		time.Duration
	runtimeClient 		runtimeapi.RuntimeServiceClient

	// TODO logReduction
}

const (
	identicalErrorDelay = 1 * time.Minute
)

func NewRemoteRuntimeService(endpoint string, connectionTimeout time.Duration) (internalapi.RuntimeService, error) {
	klog.Infof("Connecting to runtime service %s", endpoint)

	addr, dailer, err := util.GetAddressAndDialer(endpoint)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithDialer(dailer), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgSize)))
	if err != nil {
		klog.Errorf("Connect remote runtime %s failed: %v", addr, err)
		return nil, err
	}

	return &RemoteRuntimeService{
		timeout:       connectionTimeout,
		runtimeClient: runtimeapi.NewRuntimeServiceClient(conn),
		//logReduction:  logreduction.NewLogReduction(identicalErrorDelay),
	}, nil
}
