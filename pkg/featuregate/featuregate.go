package featuregate

import (
	"fmt"
	"strings"
	"sync"
)

// FeatureGate provides methods to check state of the feature
type FeatureGate interface {
	// IsEnabled returns state of the feature,
	// if feature name is unknown will always return false
	IsEnabled(feature string) bool
	// Init set state for the features from the provided map.
	// completely removes the previous state
	Init(features map[string]bool)
	// String returns string representation of the feature state
	String() string
}

// New returns default implementation of the FeatureGate interface
func New() FeatureGate {
	return &featureGate{
		lock:  &sync.RWMutex{},
		state: map[string]bool{},
	}
}

type featureGate struct {
	lock  *sync.RWMutex
	state map[string]bool
}

// IsEnabled returns state of the feature,
// if feature name is unknown will always return false
func (fg *featureGate) IsEnabled(feature string) bool {
	fg.lock.RLock()
	defer fg.lock.RUnlock()
	return fg.state[feature]
}

// Init set state for the features from the provided map.
// completely removes the previous state
func (fg *featureGate) Init(features map[string]bool) {
	fg.lock.Lock()
	defer fg.lock.Unlock()
	fg.state = make(map[string]bool, len(features))
	for k, v := range features {
		fg.state[k] = v
	}
}

// String returns string representation of the feature state
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
