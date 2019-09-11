package controller

import (
	"github.com/openshift/sriov-network-operator/pkg/controller/sriovnetwork"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerGlobalFuncs = append(AddToManagerGlobalFuncs, sriovnetwork.Add)
}
