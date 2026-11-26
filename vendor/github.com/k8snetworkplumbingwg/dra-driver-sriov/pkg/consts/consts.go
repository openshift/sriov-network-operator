/*
 * Copyright 2025 The Kubernetes Authors.
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

package consts

import (
	"time"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/dynamic-resource-allocation/deviceattribute"
)

const (
	GroupName                  = "sriovnetwork.k8snetworkplumbingwg.io"
	DriverName                 = "sriovnetwork.k8snetworkplumbingwg.io"
	DriverPluginCheckpointFile = "checkpoint.json"
	MultusAttributePrefix      = "k8s.cni.cncf.io"

	AttributePciAddress         = DriverName + "/pciAddress"
	AttributePFName             = DriverName + "/PFName"
	AttributeEswitchMode        = DriverName + "/EswitchMode"
	AttributeVendorID           = DriverName + "/vendor"
	AttributeDeviceID           = DriverName + "/deviceID"
	AttributePFDeviceID         = DriverName + "/pfDeviceID"
	AttributeVFID               = DriverName + "/vfID"
	AttributeResourceName       = DriverName + "/resourceName"
	AttributeLinkType           = DriverName + "/linkType"
	AttributeRDMACapable        = DriverName + "/rdmaCapable"
	AttributeInterfaceName      = DriverName + "/interfaceName"
	AttributeMultusDeviceID     = MultusAttributePrefix + "/deviceID"
	AttributeMultusResourceName = MultusAttributePrefix + "/resourceName"
	// Use upstream Kubernetes standard attribute prefix for pciAddress
	AttributeStandardPciAddress = deviceattribute.StandardDeviceAttributePrefix + "pciBusID"
	// AttributePfPciAddress is for the PCI address of the Physical Function (PF).
	AttributePfPciAddress = DriverName + "/pfPciAddress"

	// this is the most-common nonstandard prefix, supported by dranet and dracpu
	DraNetCompatPrefix = "dra.net"
	AttributeNUMANode  = DraNetCompatPrefix + "/numaNode"

	// Network device constants
	NetClass  = 0x02 // Network controller class
	SysBusPci = "/sys/bus/pci/devices"

	// Eswitch mode constants (as reported by devlink)
	EswitchModeLegacy    = "legacy"
	EswitchModeSwitchdev = "switchdev"

	// Link type constants
	LinkTypeEth        = "eth"
	LinkTypeEthernet   = "ethernet"
	LinkTypeIB         = "ib"
	LinkTypeInfiniband = "infiniband"
	LinkTypeUnknown    = "unknown"

	// RDMA device constants
	SysClassInfiniband = "/sys/class/infiniband"
)

// Kubernetes standard attributes
var (
	// AttributePCIeRoot identifies the PCIe root complex of the device
	AttributePCIeRoot resourceapi.QualifiedName = deviceattribute.StandardDeviceAttributePCIeRoot
)

// ReservedAttributes is the set of attribute keys populated by driver discovery
// (DiscoverSriovDevices). Policy-defined DeviceAttributes must NOT override these
// keys — any attempt will be silently skipped with a warning log.
var ReservedAttributes = map[resourceapi.QualifiedName]bool{
	AttributeVendorID:           true,
	AttributeDeviceID:           true,
	AttributePFDeviceID:         true,
	AttributePciAddress:         true,
	AttributeMultusDeviceID:     true,
	AttributePFName:             true,
	AttributeEswitchMode:        true,
	AttributeVFID:               true,
	AttributePCIeRoot:           true,
	AttributePfPciAddress:       true,
	AttributeStandardPciAddress: true,
	AttributeLinkType:           true,
	AttributeRDMACapable:        true,
	AttributeNUMANode:           true,
}

type ConfigurationMode string

const (
	ConfigurationModeStandalone ConfigurationMode = "STANDALONE"
	ConfigurationModeMultus     ConfigurationMode = "MULTUS"
)

var Backoff = wait.Backoff{
	Duration: 100 * time.Millisecond, // Initial delay
	Factor:   2.0,                    // Exponential factor
	Jitter:   0.1,                    // 10% jitter
	Steps:    5,                      // Maximum 5 attempts
	Cap:      2 * time.Second,        // Maximum delay between attempts
}
