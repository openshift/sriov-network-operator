package main

import (
	"context"
	"time"

	ocpconfigapi "github.com/openshift/api/config/v1"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var (
	namespace string
	watchTO   int
)

func init() {
	rootCmd.Flags().StringVarP(&namespace, "namespace", "n", "", "designated SriovOperatorConfig namespace")
	rootCmd.Flags().IntVarP(&watchTO, "watch-timeout", "w", 10, "sriov-operator config post-delete watch timeout ")

	// Init Scheme
	newScheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
	utilruntime.Must(sriovnetworkv1.AddToScheme(newScheme))
	utilruntime.Must(ocpconfigapi.AddToScheme(newScheme))

	vars.Scheme = newScheme
}

func runCleanupCmd(cmd *cobra.Command, args []string) error {
	// init logger
	snolog.InitLog()
	setupLog := log.Log.WithName("sriov-network-operator-config-cleanup")
	setupLog.Info("Run sriov-network-operator-config-cleanup")

	// adding context timeout although client-go Delete should be non-blocking by default
	ctx, timeoutFunc := context.WithTimeout(context.Background(), time.Second*time.Duration(watchTO))
	defer timeoutFunc()

	restConfig := ctrl.GetConfigOrDie()
	c, err := client.New(restConfig, client.Options{Scheme: vars.Scheme})
	if err != nil {
		setupLog.Error(err, "failed to create 'sriovnetworkv1' clientset")
	}

	operatorConfig := &sriovnetworkv1.SriovOperatorConfig{}
	err = c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "default"}, operatorConfig)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		setupLog.Error(err, "failed to get SriovOperatorConfig")
		return err
	}

	err = c.Delete(ctx, operatorConfig)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		setupLog.Error(err, "failed to delete SriovOperatorConfig")
		return err
	}

	for {
		err = c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "default"}, operatorConfig)
		if err != nil {
			if errors.IsNotFound(err) {
				break
			}
			setupLog.Error(err, "failed to check sriovOperatorConfig exist")
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}

	setupLog.Info("'default' SriovOperatorConfig is deleted")
	return nil
}
