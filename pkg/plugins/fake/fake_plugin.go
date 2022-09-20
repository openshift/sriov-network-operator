package fake

import (
	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

// This plugin is used in Daemon unit tests
type FakePlugin struct{}

func (f *FakePlugin) Name() string {
	return "fake_plugin"
}

func (f *FakePlugin) Spec() string {
	return "1.0"
}

func (f *FakePlugin) OnNodeStateChange(new *sriovnetworkv1.SriovNetworkNodeState) (bool, bool, error) {
	return false, false, nil
}

func (f *FakePlugin) Apply() error {
	return nil
}
