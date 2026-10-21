/*
Copyright 2026.

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
package reboottracker

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

type rebootTracker struct{}

func New() types.RebootTrackerInterface {
	return &rebootTracker{}
}

func (r *rebootTracker) GetRebootCount(generation int64) (int, error) {
	tracker, err := r.readTrackerFile()
	if err != nil {
		return 0, fmt.Errorf("failed to read reboot tracker: %w", err)
	}
	if tracker == nil || tracker.Generation != generation {
		return 0, nil
	}
	return tracker.RebootCount, nil
}

func (r *rebootTracker) IncrementRebootCounter(generation int64) error {
	tracker, err := r.readTrackerFile()
	if err != nil {
		return fmt.Errorf("failed to read reboot tracker: %w", err)
	}

	if tracker == nil || tracker.Generation != generation {
		tracker = &types.RebootTrackerFile{
			Generation:  generation,
			RebootCount: 0,
		}
	}
	tracker.RebootCount++

	if err := r.writeTrackerFile(tracker); err != nil {
		return fmt.Errorf("failed to write reboot tracker: %w", err)
	}
	return nil
}

func (r *rebootTracker) ResetRebootCounter(generation int64) error {
	tracker := &types.RebootTrackerFile{
		Generation:  generation,
		RebootCount: 0,
	}
	if err := r.writeTrackerFile(tracker); err != nil {
		return fmt.Errorf("failed to write reboot tracker: %w", err)
	}
	return nil
}

func (r *rebootTracker) readTrackerFile() (*types.RebootTrackerFile, error) {
	path := utils.GetHostExtensionPath(consts.SriovRebootTrackerFilePath)

	rawData, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("readTrackerFile(): file does not exist")
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	tracker := &types.RebootTrackerFile{}
	if err := yaml.Unmarshal(rawData, tracker); err != nil {
		return nil, fmt.Errorf("failed to unmarshal file %s: %w", path, err)
	}
	return tracker, nil
}

func (r *rebootTracker) writeTrackerFile(tracker *types.RebootTrackerFile) error {
	path := utils.GetHostExtensionPath(consts.SriovRebootTrackerFilePath)
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	out, err := yaml.Marshal(tracker)
	if err != nil {
		return fmt.Errorf("failed to marshal reboot tracker: %w", err)
	}

	log.Log.V(2).Info("writeTrackerFile(): write tracker",
		"content", string(out), "path", path)

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("failed to write temp file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("failed to rename temp file to %s: %w", path, err)
	}
	return nil
}
