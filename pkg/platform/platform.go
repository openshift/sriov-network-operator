package platform

import (
	"fmt"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platform/baremetal"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platform/openstack"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
)

//go:generate ../../bin/mockgen -destination mock/mock_platform.go -source platform.go

// Interface represents a platform abstraction that handles platform-specific operations
// for SR-IOV network configuration. Different platforms (baremetal, OpenStack, etc.) may
// have different requirements for device discovery and plugin loading.
type Interface interface {
	// Init initializes the platform-specific configuration.
	// This is called once during daemon startup after the platform is created.
	// Returns an error if initialization fails.
	Init() error

	// Name returns the name of the platform implementation (e.g., "Baremetal", "OpenStack").
	// This is used for logging and identification purposes.
	Name() string

	// DiscoverSriovDevices discovers all SR-IOV capable devices on the host.
	// This is called during status updates to get the current state of SR-IOV devices.
	// Returns a list of discovered interfaces with their SR-IOV capabilities, or an error if discovery fails.
	DiscoverSriovDevices() ([]sriovnetworkv1.InterfaceExt, error)

	// DiscoverBridges discovers software bridges managed by the operator.
	// This is called during status updates when vars.ManageSoftwareBridges is enabled.
	// Returns a list of discovered bridges, or an error if discovery fails.
	// If the platform does not support bridge discovery, it should return an empty list and error of type ErrOperationNotSupportedByPlatform.
	DiscoverBridges() (sriovnetworkv1.Bridges, error)

	// GetVendorPlugins returns the plugins to use for this platform.
	// The first return value is the main plugin that will be applied last (e.g., generic plugin).
	// The second return value is a list of additional plugins to run (e.g., vendor-specific plugins).
	// This is called during daemon initialization to load the appropriate plugins for the platform.
	// Returns the main plugin, additional plugins, and an error if plugin loading fails.
	GetVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) (plugin.VendorPlugin, []plugin.VendorPlugin, error)

	// SystemdGetVendorPlugin returns the appropriate plugin for the given systemd configuration phase.
	// This is used when the daemon runs in systemd mode.
	// phase can be consts.PhasePre (before reboot) or consts.PhasePost (after reboot).
	// Returns the plugin to use for the specified phase, or an error if the phase is invalid.
	SystemdGetVendorPlugin(phase string) (plugin.VendorPlugin, error)
}

// New creates a new platform interface based on the provided platform type.
// Returns the platform interface for the detected platform, or an error if the platform is unsupported.
func New(platformType consts.PlatformTypes, hostHelpers helper.HostHelpersInterface) (Interface, error) {
	switch platformType {
	case consts.Baremetal:
		return baremetal.New(hostHelpers)
	case consts.VirtualOpenStack:
		return openstack.New(hostHelpers)
	default:
		return nil, fmt.Errorf("unknown platform type %s", platformType)
	}
}
