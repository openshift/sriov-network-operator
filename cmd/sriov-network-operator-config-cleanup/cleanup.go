package main

import (
	"context"
	"time"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned/typed/sriovnetwork/v1"
)

var (
	namespace string
	watchTO   int
)

func init() {
	rootCmd.Flags().StringVarP(&namespace, "namespace", "n", "", "designated SriovOperatorConfig namespace")
	rootCmd.Flags().IntVarP(&watchTO, "watch-timeout", "w", 10, "sriov-operator config post-delete watch timeout ")
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
	sriovcs, err := sriovnetworkv1.NewForConfig(restConfig)
	if err != nil {
		setupLog.Error(err, "failed to create 'sriovnetworkv1' clientset")
	}

	err = sriovcs.SriovOperatorConfigs(namespace).Delete(context.Background(), "default", metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		setupLog.Error(err, "failed to delete SriovOperatorConfig")
		return err
	}

	// watching 'default' config deletion with context timeout, in case sriov-operator fails to delete 'default' config
	watcher, err := sriovcs.SriovOperatorConfigs(namespace).Watch(ctx, metav1.ListOptions{Watch: true})
	if err != nil {
		setupLog.Error(err, "failed creating 'default' SriovOperatorConfig object watcher")
		return err
	}
	defer watcher.Stop()
	for {
		select {
		case event := <-watcher.ResultChan():
			if event.Type == watch.Deleted {
				setupLog.Info("'default' SriovOperatorConfig is deleted")
				return nil
			}

		case <-ctx.Done():
			// check whether object might has been deleted before watch event triggered
			_, err := sriovcs.SriovOperatorConfigs(namespace).Get(context.Background(), "default", metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					return nil
				}
			}
			err = ctx.Err()
			setupLog.Error(err, "timeout has occurred for 'default' SriovOperatorConfig deletion")
			return err
		}
	}
}
