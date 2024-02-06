package ethtool

import (
	"github.com/safchain/ethtool"
)

func New() EthtoolLib {
	return &libWrapper{}
}

//go:generate ../../../../../bin/mockgen -destination mock/mock_ethtool.go -source ethtool.go
type EthtoolLib interface {
	// Features retrieves features of the given interface name.
	Features(ifaceName string) (map[string]bool, error)
	// FeatureNames shows supported features by their name.
	FeatureNames(ifaceName string) (map[string]uint, error)
	// Change requests a change in the given device's features.
	Change(ifaceName string, config map[string]bool) error
}

type libWrapper struct{}

// Features retrieves features of the given interface name.
func (w *libWrapper) Features(ifaceName string) (map[string]bool, error) {
	e, err := ethtool.NewEthtool()
	if err != nil {
		return nil, err
	}
	defer e.Close()
	return e.Features(ifaceName)
}

// FeatureNames shows supported features by their name.
func (w *libWrapper) FeatureNames(ifaceName string) (map[string]uint, error) {
	e, err := ethtool.NewEthtool()
	if err != nil {
		return nil, err
	}
	defer e.Close()
	return e.FeatureNames(ifaceName)
}

// Change requests a change in the given device's features.
func (w *libWrapper) Change(ifaceName string, config map[string]bool) error {
	e, err := ethtool.NewEthtool()
	if err != nil {
		return err
	}
	defer e.Close()
	return e.Change(ifaceName, config)
}
