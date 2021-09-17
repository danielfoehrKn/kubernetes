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

package util

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/kubelet/stats/pidlimit"
)

// FromApiserverCache modifies <opts> so that the GET request will
// be served from apiserver cache instead of from etcd.
func FromApiserverCache(opts *metav1.GetOptions) {
	opts.ResourceVersion = "0"
}

// GetNodenameForKernel gets hostname value to set in the hostname field (the nodename field of struct utsname) of the pod.
func GetNodenameForKernel(hostname string, hostDomainName string, setHostnameAsFQDN *bool) (string, error) {
	kernelHostname := hostname
	// FQDN has to be 64 chars to fit in the Linux nodename kernel field (specification 64 chars and the null terminating char).
	const fqdnMaxLen = 64
	if len(hostDomainName) > 0 && setHostnameAsFQDN != nil && *setHostnameAsFQDN {
		fqdn := fmt.Sprintf("%s.%s", hostname, hostDomainName)
		// FQDN has to be shorter than hostnameMaxLen characters.
		if len(fqdn) > fqdnMaxLen {
			return "", fmt.Errorf("failed to construct FQDN from pod hostname and cluster domain, FQDN %s is too long (%d characters is the max, %d characters requested)", fqdn, fqdnMaxLen, len(fqdn))
		}
		kernelHostname = fqdn
	}
	return kernelHostname, nil
}


// ParseResourceList parses the given configuration map into an API
// ResourceList or returns an error.
func ParseResourceList(m map[string]string) (v1.ResourceList, error) {
	if len(m) == 0 {
		return nil, nil
	}
	rl := make(v1.ResourceList)
	for k, v := range m {
		switch v1.ResourceName(k) {
		// CPU, memory, local storage, and PID resources are supported.
		case v1.ResourceCPU, v1.ResourceMemory, v1.ResourceEphemeralStorage, pidlimit.PIDs:
			q, err := resource.ParseQuantity(v)
			if err != nil {
				return nil, err
			}
			if q.Sign() == -1 {
				return nil, fmt.Errorf("resource quantity for %q cannot be negative: %v", k, v)
			}
			rl[v1.ResourceName(k)] = q
		default:
			return nil, fmt.Errorf("cannot reserve %q resource", k)
		}
	}
	return rl, nil
}
