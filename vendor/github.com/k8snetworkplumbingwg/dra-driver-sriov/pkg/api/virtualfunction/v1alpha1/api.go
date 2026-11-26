/*
 * Copyright 2023 The Kubernetes Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"

	"github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/consts"
)

const (
	GroupName = consts.GroupName
	Version   = "v1alpha1"

	VfConfigKind = "VfConfig"
)

// Decoder implements a decoder for objects in this API group.
var Decoder runtime.Decoder

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VFConfig holds the set of parameters for configuring a VF.
type VfConfig struct {
	metav1.TypeMeta       `json:",inline"`
	Driver                string `json:"driver,omitempty"`
	AddVhostMount         bool   `json:"addVhostMount,omitempty"`
	IfName                string `json:"ifName,omitempty"`
	NetAttachDefName      string `json:"netAttachDefName,omitempty"`
	NetAttachDefNamespace string `json:"netAttachDefNamespace,omitempty"`
}

// DefaultGpuConfig provides the default GPU configuration.
func DefaultVfConfig() *VfConfig {
	return &VfConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupName + "/" + Version,
			Kind:       VfConfigKind,
		},
		Driver:           "",
		IfName:           "",
		NetAttachDefName: "",
	}
}

// Override overrides a VfConfig config with another VfConfig config.
func (c *VfConfig) Override(other *VfConfig) {
	if other.Driver != "" {
		c.Driver = other.Driver
	}
	if other.IfName != "" {
		c.IfName = other.IfName
	}
	if other.NetAttachDefName != "" {
		c.NetAttachDefName = other.NetAttachDefName
	}
}

// Normalize updates a VfConfig config with implied default values.
// IMPLEMENT IF NEEDED
func (c *VfConfig) Normalize() {
}

//nolint:gochecknoinits // Required for Kubernetes scheme registration
func init() {
	// Create a new scheme and add our types to it. If at some point in the
	// future a new version of the configuration API becomes necessary, then
	// conversion functions can be generated and registered to continue
	// supporting older versions.
	scheme := runtime.NewScheme()
	schemeGroupVersion := schema.GroupVersion{
		Group:   GroupName,
		Version: Version,
	}
	scheme.AddKnownTypes(schemeGroupVersion,
		&VfConfig{},
	)
	metav1.AddToGroupVersion(scheme, schemeGroupVersion)

	// Set up a json serializer to decode our types.
	Decoder = json.NewSerializerWithOptions(
		json.DefaultMetaFactory,
		scheme,
		scheme,
		json.SerializerOptions{
			Pretty: true, Strict: true,
		},
	)
}
