package render

import (
	"encoding/json"
	"fmt"

	fcctbase "github.com/coreos/fcct/base/v0_1"
	translate3_1 "github.com/coreos/ignition/v2/config/v3_1/translate"
	ign3 "github.com/coreos/ignition/v2/config/v3_2"
	translate3 "github.com/coreos/ignition/v2/config/v3_2/translate"
	ign3types "github.com/coreos/ignition/v2/config/v3_2/types"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// TranspileCoreOSConfigToIgn converts CoreOS config files and systemd units to Ignition v3 config
// This is a local copy of github.com/openshift/machine-config-operator/pkg/controller/common.TranspileCoreOSConfigToIgn
// to avoid the vulnerable github.com/coreos/ignition v0.35.0 dependency.
// Only imports safe ignition/v2 packages (v3.x config formats).
func TranspileCoreOSConfigToIgn(files, units []string) (*ign3types.Config, error) {
	overwrite := true
	outConfig := ign3types.Config{}

	// Convert data to Ignition resources
	for _, contents := range files {
		f := new(fcctbase.File)
		if err := yaml.Unmarshal([]byte(contents), f); err != nil {
			return nil, fmt.Errorf("failed to unmarshal %q into struct: %w", contents, err)
		}
		f.Overwrite = &overwrite

		// Add the file to the config
		var ctCfg fcctbase.Config
		ctCfg.Storage.Files = append(ctCfg.Storage.Files, *f)
		ign3_0config, tSet, err := ctCfg.ToIgn3_0()
		if err != nil {
			return nil, fmt.Errorf("failed to transpile config to Ignition config %w\nTranslation set: %v", err, tSet)
		}
		// Translate from 3.0 -> 3.1 -> 3.2 using built-in translate functions
		ign3_2config := translate3.Translate(translate3_1.Translate(ign3_0config))
		outConfig = ign3.Merge(outConfig, ign3_2config)
	}

	for _, contents := range units {
		u := new(fcctbase.Unit)
		if err := yaml.Unmarshal([]byte(contents), u); err != nil {
			return nil, fmt.Errorf("failed to unmarshal systemd unit into struct: %w", err)
		}

		// Add the unit to the config
		var ctCfg fcctbase.Config
		ctCfg.Systemd.Units = append(ctCfg.Systemd.Units, *u)
		ign3_0config, tSet, err := ctCfg.ToIgn3_0()
		if err != nil {
			return nil, fmt.Errorf("failed to transpile config to Ignition config %w\nTranslation set: %v", err, tSet)
		}
		// Translate from 3.0 -> 3.1 -> 3.2 using built-in translate functions
		ign3_2config := translate3.Translate(translate3_1.Translate(ign3_0config))
		outConfig = ign3.Merge(outConfig, ign3_2config)
	}

	return &outConfig, nil
}

// MachineConfigFromIgnConfig creates a MachineConfig with the provided Ignition config
// This is a local copy of github.com/openshift/machine-config-operator/pkg/controller/common.MachineConfigFromIgnConfig
// to avoid the vulnerable github.com/coreos/ignition v0.35.0 dependency.
func MachineConfigFromIgnConfig(role, name string, ignCfg interface{}) (*mcfgv1.MachineConfig, error) {
	rawIgnCfg, err := json.Marshal(ignCfg)
	if err != nil {
		return nil, fmt.Errorf("error marshaling Ignition config: %w", err)
	}
	return MachineConfigFromRawIgnConfig(role, name, rawIgnCfg)
}

// MachineConfigFromRawIgnConfig creates a MachineConfig with the provided raw Ignition config
// This is a local copy of github.com/openshift/machine-config-operator/pkg/controller/common.MachineConfigFromRawIgnConfig
// to avoid the vulnerable github.com/coreos/ignition v0.35.0 dependency.
func MachineConfigFromRawIgnConfig(role, name string, rawIgnCfg []byte) (*mcfgv1.MachineConfig, error) {
	labels := map[string]string{
		mcfgv1.MachineConfigRoleLabelKey: role,
	}
	return &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
			Name:   name,
		},
		Spec: mcfgv1.MachineConfigSpec{
			OSImageURL: "",
			Config: runtime.RawExtension{
				Raw: rawIgnCfg,
			},
		},
	}, nil
}
