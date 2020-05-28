package remote


import (
	"time"
	internalapi "k8s.io/cri-api/pkg/apis"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/util"
	"context"
	"google.golang.org/grpc"
)

type RemoteImageService struct {
	timeout 		time.Duration
	imageClient 		runtimeapi.ImageServiceClient
}

func NewRemoteImageService(endpoint string, connectionTimeout time.Duration) (internalapi.ImageManagerService, error) {
	klog.Infof("Connecting to image service %s", endpoint)

	addr, dailer, err := util.GetAddressAndDialer(endpoint)

	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithDialer(dailer), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgSize)))

	if err != nil {
		klog.Errorf("Connect remote image service %s failed: %v", addr, err)
		return nil, err
	}

	return &RemoteImageService{
		timeout: 	connectionTimeout,
		imageClient:	runtimeapi.NewImageServiceClient(conn),
	}, nil
}

func (r *RemoteImageService) ListImages(filter *runtimeapi.ImageFilter) ([]*runtimeapi.Image, error) {
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	resp, err := r.imageClient.ListImages(ctx, &runtimeapi.ListImagesRequest{
		Filter: filter,
	})
	if err != nil {
		klog.Errorf("ListImages with filter %+v from image service failed: %v", filter, err)
		return nil, err
	}

	return resp.Images, nil
}