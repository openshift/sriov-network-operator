package types

import (
	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

// Service contains info about systemd service
type Service struct {
	Name    string
	Path    string
	Content string
}

// ServiceInjectionManifestFile service injection manifest file structure
type ServiceInjectionManifestFile struct {
	Name    string
	Dropins []struct {
		Contents string
	}
}

// ServiceManifestFile service manifest file structure
type ServiceManifestFile struct {
	Name     string
	Contents string
}

// ScriptManifestFile script manifest file structure
type ScriptManifestFile struct {
	Path     string
	Contents struct {
		Inline string
	}
}

// SriovConfig: Contains the information we saved on the host for the sriov-config service running on the host
type SriovConfig struct {
	Spec                  sriovnetworkv1.SriovNetworkNodeStateSpec `yaml:"spec"`
	UnsupportedNics       bool                                     `yaml:"unsupportedNics"`
	PlatformType          consts.PlatformTypes                     `yaml:"platformType"`
	ManageSoftwareBridges bool                                     `yaml:"manageSoftwareBridges"`
	OVSDBSocketPath       string                                   `yaml:"ovsdbSocketPath"`
}

// SriovResult: Contains the result from the sriov-config service trying to apply the requested policies
type SriovResult struct {
	SyncStatus    string `yaml:"syncStatus"`
	LastSyncError string `yaml:"lastSyncError"`
}
