package daemon

import (
	"fmt"
	"path/filepath"
	"plugin"

	"github.com/golang/glog"
	sriovnetworkv1 "github.com/pliurh/sriov-network-operator/pkg/apis/sriovnetwork/v1"
)

type VendorPlugin interface {
	// Return the name of plugin
	Name() string
	// Return the SpecVersion followed by plugin
	Spec() string
	// Invoked when SriovNetworkNodeState CR is created, return if need dain and/or reboot node
	OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (bool, bool, error)
	// Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
	OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (bool, bool, error)
	// Apply config change
	Apply() error
}

var pluginMap = map[string]string{
	"8086": "intel_plugin",
	"15b3": "mellanox_plugin",
}

const (
	SpecVersion   = "1.0"
	GenericPlugin = "generic_plugin"
)

func loadVendorPlugins(path string) (map[string]VendorPlugin, error) {
	pluginList := map[string]VendorPlugin{}
	allPlugins, err := filepath.Glob(path + "/*.so")
	if err != nil {
		glog.Error("fail to list plugins from path %s: %v", path, err)
	}

	for _, filename := range allPlugins {
		glog.Infof("loadVendorPlugins(): try to load plugin %s", filename)
		p, err := loadOnePlugin(filename)
		if err != nil {
			glog.Error("fail to load plugin %s: %e", filename, err)
		}
		pluginList[p.Name()] = p
	}

	return pluginList, nil
}

// loadPlugin loads a single plugin from a file path
func loadOnePlugin(path string) (VendorPlugin, error) {
	plug, err := plugin.Open(path)
	if err != nil {
		return nil, err
	}

	symbol, err := plug.Lookup("Plugin")
	if err != nil {
		return nil, err
	}

	// Cast the loaded symbol to the VendorPlugin
	p, ok := symbol.(VendorPlugin)
	if !ok {
		return nil, fmt.Errorf("Unable to load plugin")
	}

	// Check the spec to ensure we are supported version
	if p.Spec() != SpecVersion {
		return nil, fmt.Errorf("Spec mismatch")
	}

	return p, nil
}
