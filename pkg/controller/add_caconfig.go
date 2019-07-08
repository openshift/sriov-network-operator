package controller

import (
	"github.com/openshift/sriov-network-operator/pkg/controller/caconfig"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, caconfig.Add)
}
