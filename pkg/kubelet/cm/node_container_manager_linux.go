//go:build linux
// +build linux

/*
Copyright 2017 The Kubernetes Authors.

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

package cm

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog/v2"
	kubefeatures "k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/events"
	"k8s.io/kubernetes/pkg/kubelet/stats/pidlimit"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
)

const (
	defaultNodeAllocatableCgroupName = "kubepods"
)

//createNodeAllocatableCgroups creates Node Allocatable Cgroup when CgroupsPerQOS flag is specified as true
// creates the /kubepods cgroup for each resource subsystem
func (cm *containerManagerImpl) createNodeAllocatableCgroups() error {
	nodeAllocatable := cm.internalCapacity
	// Use Node Allocatable limits instead of capacity if the user requested enforcing node allocatable.
	nc := cm.NodeConfig.NodeAllocatableConfig
	if cm.CgroupsPerQOS && nc.EnforceNodeAllocatable.Has(kubetypes.NodeAllocatableEnforcementKey) {
		nodeAllocatable = cm.getNodeAllocatableInternalAbsolute()
	}

	cgroupConfig := &CgroupConfig{
		// THIS ALREADY is  "root/kubepods"  --> "/kubepods"
		Name: cm.cgroupRoot,
		// The default limits for cpu shares can be very low which can lead to CPU starvation for pods.
		ResourceParameters: getCgroupConfig(nodeAllocatable),
	}
	// cgroup manager Exists() checks if this cgroup exists for every single resource controller in cgroupsV1!!!
	if cm.cgroupManager.Exists(cgroupConfig.Name) {
		return nil
	}

	// does it create for  each resource controller? Would be handy!
	// yes it does: please check cgroup_manager_linux!
	if err := cm.cgroupManager.Create(cgroupConfig); err != nil {
		klog.ErrorS(err, "Failed to create cgroup", "cgroupName", cm.cgroupRoot)
		return err
	}
	return nil
}

// enforceNodeAllocatableCgroups enforce Node Allocatable Cgroup settings.
func (cm *containerManagerImpl) enforceNodeAllocatableCgroups() error {
	nc := cm.NodeConfig.NodeAllocatableConfig

	// We need to update limits on node allocatable cgroup no matter what because
	// default cpu shares on cgroups are low and can cause cpu starvation.
	nodeAllocatable := cm.internalCapacity
	// Use Node Allocatable limits instead of capacity if the user requested enforcing node allocatable.
	if cm.CgroupsPerQOS && nc.EnforceNodeAllocatable.Has(kubetypes.NodeAllocatableEnforcementKey) {
		// Allocatable per resource (capacity  - kube-reserved - system-reserved - eviction-hard)
		nodeAllocatable = cm.getNodeAllocatableInternalAbsolute()
	}

	klog.V(4).InfoS("Attempting to enforce Node Allocatable", "config", nc)

	cgroupConfig := &CgroupConfig{
		// THIS ALREADY is  "root/kubepods"  --> "/kubepods"
		Name:               cm.cgroupRoot,
		ResourceParameters: getCgroupConfig(nodeAllocatable),
	}

	// Using ObjectReference for events as the node maybe not cached; refer to #42701 for detail.
	nodeRef := &v1.ObjectReference{
		Kind:      "Node",
		Name:      cm.nodeInfo.Name,
		UID:       types.UID(cm.nodeInfo.Name),
		Namespace: "",
	}

	// If Node Allocatable is enforced on a node that has not been drained or is updated on an existing node to a lower value,
	// existing memory usage across pods might be higher than current Node Allocatable Memory Limits.
	// Pod Evictions are expected to bring down memory usage to below Node Allocatable limits.
	// Until evictions happen retry cgroup updates.
	// Update limits on non root cgroup-root to be safe since the default limits for CPU can be too low.
	// Check if cgroupRoot is set to a non-empty value (empty would be the root container)

	// TODO: Two things to update
	// 0) Add a periodic task that fetches & updates the kube reserved
	//    - should update cm.NodeConfig.KubeReserved
	//    - can be done like here: https://github.com/kubernetes/kubernetes/blob/481cf6fbe753b9eb2a47ced179211206b0a99540/pkg/kubelet/cm/container_manager_linux.go#L543
	// 1)  update the setter that is used to calculate the Node status's Allocatable (requested peridoically every 10s)
	//     - should use mutex as multiple threads
	//     -> it is just down in the same file: https://github.com/kubernetes/kubernetes/blob/481cf6fbe753b9eb2a47ced179211206b0a99540/pkg/kubelet/cm/node_container_manager_linux.go#L228
	// 2) Update below function that resets the kubepods cgroup limits every one minute back to the inital config in "cgroupConfig"
	//    - instead  of the static "cgroupConfig" it should be calculated each time based on "cm.NodeConfig.KubeReserved"
	//       - just call cm.getNodeAllocatableInternalAbsolute() again with mutex to pass into "getCgroupConfig(nodeAllocatable)"
	//    - the enforceNodeAllocatableCgroups() is only called once during the kubelet startup in "setupNode()"
	// 3) Also needs to be done for --system-reserved-cgroup and  --kube-reserved-cgroup below
	//  - however, I do not get why that is not done  periodically like for the kubepods cgroup
	// 4) What about the eviction manager? Def. also influenced by new kube-reserved
	// 5) Update the static allocatables handed over dduring setupNode() to
	//  - MemoryManager feature gate: https://github.com/kubernetes/kubernetes/blob/481cf6fbe753b9eb2a47ced179211206b0a99540/pkg/kubelet/cm/container_manager_linux.go#L355
	//  - CPUManager feature gate: https://github.com/kubernetes/kubernetes/blob/481cf6fbe753b9eb2a47ced179211206b0a99540/pkg/kubelet/cm/container_manager_linux.go#L340
	//   --> Do that in a later step (quite some work with all the testing)!
	if len(cm.cgroupRoot) > 0 {
		go func() {
			for {
				err := cm.cgroupManager.Update(cgroupConfig)
				if err == nil {
					cm.recorder.Event(nodeRef, v1.EventTypeNormal, events.SuccessfulNodeAllocatableEnforcement, "Updated Node Allocatable limit across pods")
					return
				}
				message := fmt.Sprintf("Failed to update Node Allocatable Limits %q: %v", cm.cgroupRoot, err)
				cm.recorder.Event(nodeRef, v1.EventTypeWarning, events.FailedNodeAllocatableEnforcement, message)
				time.Sleep(time.Minute)
			}
		}()
	}
	// Now apply kube reserved and system reserved limits if required.
	if nc.EnforceNodeAllocatable.Has(kubetypes.SystemReservedEnforcementKey) {
		klog.V(2).InfoS("Enforcing system reserved on cgroup", "cgroupName", nc.SystemReservedCgroupName, "limits", nc.SystemReserved)
		if err := enforceExistingCgroup(cm.cgroupManager, cm.cgroupManager.CgroupName(nc.SystemReservedCgroupName), nc.SystemReserved); err != nil {
			message := fmt.Sprintf("Failed to enforce System Reserved Cgroup Limits on %q: %v", nc.SystemReservedCgroupName, err)
			cm.recorder.Event(nodeRef, v1.EventTypeWarning, events.FailedNodeAllocatableEnforcement, message)
			return fmt.Errorf(message)
		}
		cm.recorder.Eventf(nodeRef, v1.EventTypeNormal, events.SuccessfulNodeAllocatableEnforcement, "Updated limits on system reserved cgroup %v", nc.SystemReservedCgroupName)
	}

	// This is not triggered on my Shoot cluster
	// YES! Because gardener does not configure a seperate cgroup for the kubernetes daemons via --kube-reserved-cgroup (all under system.slice)

	// +..systemreserved or system.slice (Specified via `--system-reserved-cgroup`; `SystemReserved` enforced here *optionally* by kubelet)
	// .        .    .tasks(sshd,udev,etc)
	// +..podruntime or podruntime.slice (Specified via `--kube-reserved-cgroup`; `KubeReserved` enforced here *optionally* by kubelet)
	// .	 +..kubelet
	// .	 .   .tasks(kubelet)
	// .	 +..runtime
	// .	     .tasks(docker-engine, containerd)
	// .
	// +..kubepods or kubepods.slice (Node Allocatable enforced here by Kubelet)
	// contains values from --kube-reserved-cgroup which is empty in Gardener case
	if nc.EnforceNodeAllocatable.Has(kubetypes.KubeReservedEnforcementKey) {
		klog.V(2).InfoS("Enforcing kube reserved on cgroup", "cgroupName", nc.KubeReservedCgroupName, "limits", nc.KubeReserved)
		// this actually sets the kube-reserved on the kubepods cgroup
		// How is it that the root cgroup already contains  /kubepods, what  is in nc.KubeReservedCgroupName ?
		if err := enforceExistingCgroup(cm.cgroupManager, cm.cgroupManager.CgroupName(nc.KubeReservedCgroupName), nc.KubeReserved); err != nil {
			message := fmt.Sprintf("Failed to enforce Kube Reserved Cgroup Limits on %q: %v", nc.KubeReservedCgroupName, err)
			cm.recorder.Event(nodeRef, v1.EventTypeWarning, events.FailedNodeAllocatableEnforcement, message)
			return fmt.Errorf(message)
		}
		cm.recorder.Eventf(nodeRef, v1.EventTypeNormal, events.SuccessfulNodeAllocatableEnforcement, "Updated limits on kube reserved cgroup %v", nc.KubeReservedCgroupName)
	}
	return nil
}

// enforceExistingCgroup updates the limits `rl` on existing cgroup `cName` using `cgroupManager` interface.
func enforceExistingCgroup(cgroupManager CgroupManager, cName CgroupName, rl v1.ResourceList) error {
	rp := getCgroupConfig(rl)

	// Enforce MemoryQoS for cgroups of kube-reserved/system-reserved. For more information,
	// see https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/2570-memory-qos
	if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.MemoryQoS) {
		if rp.Memory != nil {
			if rp.Unified == nil {
				rp.Unified = make(map[string]string)
			}
			rp.Unified[MemoryMin] = strconv.FormatInt(*rp.Memory, 10)
		}
	}

	cgroupConfig := &CgroupConfig{
		Name:               cName,
		ResourceParameters: rp,
	}
	if cgroupConfig.ResourceParameters == nil {
		return fmt.Errorf("%q cgroup is not config properly", cgroupConfig.Name)
	}
	klog.V(4).InfoS("Enforcing limits on cgroup", "cgroupName", cName, "cpuShares", cgroupConfig.ResourceParameters.CpuShares, "memory", cgroupConfig.ResourceParameters.Memory, "pidsLimit", cgroupConfig.ResourceParameters.PidsLimit)
	if !cgroupManager.Exists(cgroupConfig.Name) {
		return fmt.Errorf("%q cgroup does not exist", cgroupConfig.Name)
	}
	if err := cgroupManager.Update(cgroupConfig); err != nil {
		return err
	}
	return nil
}

// getCgroupConfig returns a ResourceConfig object that can be used to create or update cgroups via CgroupManager interface.
func getCgroupConfig(rl v1.ResourceList) *ResourceConfig {
	// TODO(vishh): Set CPU Quota if necessary.
	if rl == nil {
		return nil
	}
	var rc ResourceConfig
	if q, exists := rl[v1.ResourceMemory]; exists {
		// Memory is defined in bytes.
		val := q.Value()
		rc.Memory = &val
	}
	if q, exists := rl[v1.ResourceCPU]; exists {
		// CPU is defined in milli-cores.
		val := MilliCPUToShares(q.MilliValue())
		rc.CpuShares = &val
	}
	if q, exists := rl[pidlimit.PIDs]; exists {
		val := q.Value()
		rc.PidsLimit = &val
	}
	rc.HugePageLimit = HugePageLimits(rl)

	return &rc
}

// GetNodeAllocatableAbsolute returns the absolute value of Node Allocatable which is primarily useful for enforcement.
// Note that not all resources that are available on the node are included in the returned list of resources.
// Returns a ResourceList.
func (cm *containerManagerImpl) GetNodeAllocatableAbsolute() v1.ResourceList {
	return cm.getNodeAllocatableAbsoluteImpl(cm.capacity)
}

func (cm *containerManagerImpl) getNodeAllocatableAbsoluteImpl(capacity v1.ResourceList) v1.ResourceList {
	cm.RLock()
	defer cm.RUnlock()

	return getNodeAllocatableAbsoluteImpl(capacity, cm.NodeConfig.SystemReserved, cm.NodeConfig.KubeReserved)
}

// getNodeAllocatableAbsoluteImpl returns the absolute value of Node Allocatable (without eviction threashold)
// calculated from the given Node's capacity, system- and kube-reserved
// Returns a ResourceList.
func getNodeAllocatableAbsoluteImpl(capacity v1.ResourceList, systemReserved, kubeReserved v1.ResourceList) v1.ResourceList {
	result := make(v1.ResourceList)
	for k, v := range capacity {
		value := v.DeepCopy()
		if systemReserved != nil {
			value.Sub(systemReserved[k])
		}
		if kubeReserved != nil {
			value.Sub(kubeReserved[k])
		}
		if value.Sign() < 0 {
			// Negative Allocatable resources don't make sense.
			value.Set(0)
		}
		result[k] = value
	}
	return result
}

// getNodeAllocatableInternalAbsolute is similar to getNodeAllocatableAbsolute except that
// it also includes internal resources (currently process IDs).  It is intended for setting
// up top level cgroups only.
func (cm *containerManagerImpl) getNodeAllocatableInternalAbsolute() v1.ResourceList {
	return cm.getNodeAllocatableAbsoluteImpl(cm.internalCapacity)
}

// GetNodeAllocatableReservation returns amount of compute or storage resource that have to be reserved on this node from scheduling.
// TODO D060239: This is the setter function called during the periodic sync of the node status !!!
// from  here: https://github.com/kubernetes/kubernetes/blob/0cd75e8fec62a2531637e80bb950ac9983cac1b0/pkg/kubelet/nodestatus/setters.go#L375
//  calculate the desired Node Allocatable
// ADJUSTING THIS DYNAMICALLY WILL CAUSE THE NODE STATUS TO BE UPDATED!
func (cm *containerManagerImpl) GetNodeAllocatableReservation() v1.ResourceList {
	cm.RLock()
	defer cm.RUnlock()

	evictionReservation := hardEvictionReservation(cm.HardEvictionThresholds, cm.capacity)
	result := make(v1.ResourceList)
	for k := range cm.capacity {
		value := resource.NewQuantity(0, resource.DecimalSI)
		if cm.NodeConfig.SystemReserved != nil {
			value.Add(cm.NodeConfig.SystemReserved[k])
		}
		if cm.NodeConfig.KubeReserved != nil {
			value.Add(cm.NodeConfig.KubeReserved[k])
		}
		if evictionReservation != nil {
			value.Add(evictionReservation[k])
		}
		if !value.IsZero() {
			result[k] = *value
		}
	}
	return result
}

func (cm *containerManagerImpl) UpdateResourceReservations(systemReserved, kubeReserved v1.ResourceList) error {
	allocatable := getNodeAllocatableAbsoluteImpl(cm.capacity, systemReserved, kubeReserved)

	// validate against the allocatable without eviction
	if err := validateNodeAllocatable(cm.capacity, allocatable); err != nil {
		return fmt.Errorf("unable to update resource reservations: %v", err)
	}

	// TODO: validate if it actually changing anything
	// ---> reduce Event noise!!!


	cm.RLock()
	defer cm.RUnlock()
	cm.NodeConfig.SystemReserved = systemReserved
	cm.NodeConfig.KubeReserved = kubeReserved

	if !cm.CgroupsPerQOS {
		return fmt.Errorf("cannot update resource reservations when cgroups per QOS is not enabled")
	}

	if !cm.NodeConfig.NodeAllocatableConfig.EnforceNodeAllocatable.Has(kubetypes.NodeAllocatableEnforcementKey) {
		return fmt.Errorf("cannot update resource reservations when node allocatable for pods is not enforced")
	}

	if !cm.cgroupManager.Exists(cm.cgroupRoot) {
		return fmt.Errorf("cgroup root %q does not exit", cm.cgroupRoot)
	}

	cgroupConfig := &CgroupConfig{
		// THIS ALREADY is  "root/kubepods"  --> "/kubepods"
		Name:               cm.cgroupRoot,
		ResourceParameters: getCgroupConfig(cm.getNodeAllocatableInternalAbsolute()),
	}

	// Using ObjectReference for events as the node maybe not cached; refer to #42701 for detail.
	nodeRef := &v1.ObjectReference{
		Kind:      "Node",
		Name:      cm.nodeInfo.Name,
		UID:       types.UID(cm.nodeInfo.Name),
		Namespace: "",
	}

	fmt.Printf("DynamicResourceReservations: attempting to enforce updated Node Allocatable")
	err := cm.cgroupManager.Update(cgroupConfig)
	if err == nil {
		cm.recorder.Event(nodeRef, v1.EventTypeNormal, events.SuccessfulNodeAllocatableEnforcement, "Updated Node Allocatable limit across pods")
		return nil
	}
	message := fmt.Sprintf("Failed to update Node Allocatable Limits %q: %v", cm.cgroupRoot, err)
	cm.recorder.Event(nodeRef, v1.EventTypeWarning, events.FailedNodeAllocatableEnforcement, message)

	return fmt.Errorf(message)
}

// validateNodeAllocatable ensures that the user specified Node Allocatable Configuration doesn't reserve more than the node capacity.
// Returns error if the configuration is invalid, nil otherwise.
func (cm *containerManagerImpl) validateNodeAllocatable() error {
	nar := cm.GetNodeAllocatableReservation()
	return validateNodeAllocatable(cm.capacity, nar)
}

// validateNodeAllocatable ensures that the  specified Node Allocatable Configuration
// does not reserve more memory than the given capacity.
// Returns error if the configuration is invalid, nil otherwise.
func validateNodeAllocatable(capacity, nodeAllocatableReservation v1.ResourceList) error {
	var errors []string
	for k, v := range nodeAllocatableReservation {
		value := capacity[k]
		value.Sub(v)

		if value.Sign() < 0 {
			errors = append(errors, fmt.Sprintf("Resource %q has an allocatable of %v, capacity of %v", k, v, value))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("invalid Node Allocatable configuration. %s", strings.Join(errors, " "))
	}
	return nil
}
