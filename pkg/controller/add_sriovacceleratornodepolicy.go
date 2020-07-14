package controller

import (
	"github.com/openshift/sriov-network-operator/pkg/controller/sriovacceleratornodepolicy"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, sriovacceleratornodepolicy.Add)
}
