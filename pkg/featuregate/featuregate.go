package featuregate

import (
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

// DefaultFeatureStates contains the default states for the feature gates
var DefaultFeatureStates = map[string]bool{
	consts.ParallelNicConfigFeatureGate:                false,
	consts.ResourceInjectorMatchConditionFeatureGate:   false,
	consts.MetricsExporterFeatureGate:                  false,
	consts.ManageSoftwareBridgesFeatureGate:            false,
	consts.BlockDevicePluginUntilConfiguredFeatureGate: true,
	consts.MellanoxFirmwareResetFeatureGate:            true,
	consts.DynamicResourceAllocationFeatureGate:        false, // Default: use device plugin
}

// FeatureGate provides methods to check state of the feature
type FeatureGate interface {
	// IsEnabled returns state of the feature,
	// if feature name is unknown will always return false
	IsEnabled(feature string) bool
	// Set applies the provided overrides to the default feature states.
	// It returns true only when the resulting state changes.
	Set(features map[string]bool) bool
	// String returns string representation of the feature state
	String() string
}

// New returns default implementation of the FeatureGate interface with the default features state
func New() FeatureGate {
	return &featureGate{
		lock:            &sync.RWMutex{},
		state:           map[string]bool{},
		defaultFeatures: DefaultFeatureStates,
	}
}

// NewWithDefaultFeatures returns a new FeatureGate with the default features state explicitly set
func NewWithDefaultFeatures(defaultFeatures map[string]bool) FeatureGate {
	return &featureGate{
		lock:            &sync.RWMutex{},
		state:           map[string]bool{},
		defaultFeatures: defaultFeatures,
	}
}

type featureGate struct {
	lock            *sync.RWMutex
	state           map[string]bool
	defaultFeatures map[string]bool
}

// IsEnabled returns state of the feature,
// if feature name is unknown will always return false
func (fg *featureGate) IsEnabled(feature string) bool {
	fg.lock.RLock()
	defer fg.lock.RUnlock()
	return fg.state[feature]
}

// Set applies overrides only when they change the effective feature state.
func (fg *featureGate) Set(features map[string]bool) bool {
	fg.lock.Lock()
	defer fg.lock.Unlock()
	if fg.matches(features) {
		return false
	}
	state := maps.Clone(fg.defaultFeatures)
	if state == nil {
		state = map[string]bool{}
	}
	maps.Copy(state, features)
	fg.state = state
	return true
}

// matches compares the defaults plus overrides with the active state without
// allocating another map. The caller must hold fg.lock.
func (fg *featureGate) matches(features map[string]bool) bool {
	featureCount := len(fg.defaultFeatures)
	for feature, enabled := range features {
		if current, exists := fg.state[feature]; !exists || current != enabled {
			return false
		}
		if _, hasDefault := fg.defaultFeatures[feature]; !hasDefault {
			featureCount++
		}
	}
	if len(fg.state) != featureCount {
		return false
	}
	for feature, enabled := range fg.defaultFeatures {
		if _, overridden := features[feature]; overridden {
			continue
		}
		if current, exists := fg.state[feature]; !exists || current != enabled {
			return false
		}
	}
	return true
}

// String returns string representation of the features state
func (fg *featureGate) String() string {
	fg.lock.RLock()
	defer fg.lock.RUnlock()
	var result strings.Builder
	var sep string
	for k, v := range fg.state {
		result.WriteString(fmt.Sprintf("%s%s:%t", sep, k, v))
		sep = ", "
	}
	return result.String()
}
