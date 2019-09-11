package controller

import (
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// AddToManagerFuncs is a list of functions to add Controllers to the Manager
var AddToManagerFuncs []func(manager.Manager) error

// AddToManagerGlobalFuncs is a list of functions to add Controllers to the Global Manager
var AddToManagerGlobalFuncs []func(manager.Manager) error

// AddToManager adds Controllers to the Manager
func AddToManager(m manager.Manager) error {
	for _, f := range AddToManagerFuncs {
		if err := f(m); err != nil {
			return err
		}
	}
	return nil
}

// AddToManagerGlobal adds Controllers to the Global Manager
func AddToManagerGlobal(m manager.Manager) error {
	for _, f := range AddToManagerGlobalFuncs {
		if err := f(m); err != nil {
			return err
		}
	}
	return nil
}