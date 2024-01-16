package types

import (
	"github.com/coreos/go-systemd/v22/unit"
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

var (
	// Remove run condition form the service
	ConditionOpt = &unit.UnitOption{
		Section: "Unit",
		Name:    "ConditionPathExists",
		Value:   "!/etc/ignition-machine-config-encapsulated.json",
	}
)
