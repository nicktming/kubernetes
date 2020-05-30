package cm

import (
	v1 "k8s.io/api/core/v1"
)

type ContainerManager interface {


	GetCapacity()				v1.ResourceList

	GetDevicePluginResourceCapacity() 	(v1.ResourceList, v1.ResourceList, []string)

	GetNodeAllocatableReservation()		v1.ResourceList
}