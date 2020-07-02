package controller

import (
	"github.com/openshift/sriov-network-operator/pkg/controller/sriovibnetwork"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerGlobalFuncs = append(AddToManagerGlobalFuncs, sriovibnetwork.Add)
}
