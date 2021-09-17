/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resourcereservations

import (
	"context"
	"fmt"
	"log"

	resourcereservationsv1alpha1 "github.com/danielfoehrkn/resource-reservations-grpc/pkg/proto/gen/resource-reservations"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/util"
)

// ReservationsServer implements ReservationsServer
type ReservationsServer struct {
	updater ResourceReservationsUpdater
}

// NewV1ResourceReservationServer returns a ResourceReservationsServer which updates kubelet resource requests
func NewV1ResourceReservationServer(updater ResourceReservationsUpdater) resourcereservationsv1alpha1.ResourceReservationsServer{
	return &ReservationsServer{
		updater: updater,
	}
}

func (s *ReservationsServer) UpdateResourceReservations(ctx context.Context, request *resourcereservationsv1alpha1.UpdateResourceReservationsRequest) (*resourcereservationsv1alpha1.UpdateResourceReservationsResponse, error) {
	log.Println(fmt.Sprintf("Request kube-reserved: %v", request.KubeReserved))
	log.Println(fmt.Sprintf("Request system-reserved: %v", request.SystemReserved))

	kubeReserved, err := util.ParseResourceList(request.KubeReserved)
	if err != nil {
		fmt.Printf("UpdateResourceReservations: failed to parse system reserved: %v", kubeReserved)
		return nil, err
	}
	systemReserved, err := util.ParseResourceList(request.SystemReserved)
	if err != nil {
		fmt.Printf("UpdateResourceReservations: failed to parse system reserved: %v", systemReserved)
		return nil, err
	}

	if err := s.updater.UpdateResourceReservations(systemReserved, kubeReserved); err != nil {
		return nil, err
	}

	klog.Infof("DynamicResourceReservations: successfully updated resource reservations")

	// TODO: possibly add error message if validation failed
	return &resourcereservationsv1alpha1.UpdateResourceReservationsResponse{}, nil
}
