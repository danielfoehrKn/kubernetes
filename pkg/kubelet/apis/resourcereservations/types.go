/*
Copyright 2020 The Kubernetes Authors.

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
	"k8s.io/api/core/v1"
)

// TODO: do we really need that updater interface?
// we already hand over the whole container manager already need for the GetResourceReservations()

// ResourceReservationsUpdater knows how to update resource reservations
type ResourceReservationsUpdater interface {
	// UpdateResourceReservations updates the system- and kube-reserved settings which influence Node allocatable
	UpdateResourceReservations(systemReserved, kubeReserved v1.ResourceList) error
}

// // ResourceReservationsUpdater knows how to get the current resource reservations
type ResourceReservationsGetter interface {
	// GetResourceReservations gets the current resource reservations from the container managers internal NodeConfig
	// Returns the system-reserved and the kube-reserved v1.ResourceList as the second argument
	GetResourceReservations() (v1.ResourceList, v1.ResourceList)
}