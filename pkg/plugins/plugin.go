package plugin

import (
	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

//go:generate ../../bin/mockgen -destination mock/mock_plugin.go -source plugin.go
type VendorPlugin interface {
	// Return the name of plugin
	Name() string
	// Return the SpecVersion followed by plugin
	Spec() string
	// Invoked when SriovNetworkNodeState CR is created or updated, return if need dain and/or reboot node
	OnNodeStateChange(new *sriovnetworkv1.SriovNetworkNodeState) (bool, bool, error)
	// Apply config change
	Apply() error
}
