/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

var (
	waitForConfigCmd = &cobra.Command{
		Use:   "wait-for-config",
		Short: "Wait for SR-IOV configuration to be applied",
		Long: "Init container command that sets annotation on pod and waits for " +
			"sriov-config-daemon to apply configuration and remove the annotation",
		RunE: runWaitForConfigCmd,
	}

	waitForConfigOpts struct {
		podName      string
		podNamespace string
	}
)

func init() {
	rootCmd.AddCommand(waitForConfigCmd)
	waitForConfigCmd.PersistentFlags().StringVar(&waitForConfigOpts.podName, "pod-name", "",
		"kubernetes pod name of the device plugin")
	waitForConfigCmd.PersistentFlags().StringVar(&waitForConfigOpts.podNamespace, "pod-namespace", "",
		"kubernetes namespace where the device plugin pod is running")
}

type WaitForConfigReconciler struct {
	client.Client
	Pod    types.NamespacedName
	Cancel context.CancelFunc
}

func (r *WaitForConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// This check is currently redundant since the cache is configured to watch only the target pod object.
	// However, it is intentionally included to document this assumption and to provide a safeguard in case
	// the cache configuration is modified in the future to watch additional pods.
	if r.Pod != req.NamespacedName {
		return ctrl.Result{}, nil
	}

	pod := &corev1.Pod{}
	if err := r.Get(ctx, req.NamespacedName, pod); err != nil {
		logger.Error(err, "Failed to get pod")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !utils.ObjectHasAnnotationKey(pod, consts.DevicePluginWaitConfigAnnotation) {
		logger.Info("Annotation removed, device plugin can proceed")
		r.Cancel()
		return ctrl.Result{}, nil
	}

	logger.Info("Annotation still present, waiting...")
	return ctrl.Result{RequeueAfter: consts.DaemonRequeueTime}, nil
}

func (r *WaitForConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}

func validateWaitForConfigOpts() error {
	if waitForConfigOpts.podName == "" {
		return fmt.Errorf("--pod-name is required")
	}
	if waitForConfigOpts.podNamespace == "" {
		return fmt.Errorf("--pod-namespace is required")
	}
	return nil
}

func runWaitForConfigCmd(cmd *cobra.Command, args []string) error {
	snolog.InitLog()
	setupLog := log.Log.WithName("wait-for-config")

	if err := validateWaitForConfigOpts(); err != nil {
		setupLog.Error(err, "invalid command line arguments")
		return err
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		setupLog.Error(err, "failed to get in-cluster config")
		return err
	}

	return startWaitForConfigManager(setupLog, config, types.NamespacedName{Name: waitForConfigOpts.podName, Namespace: waitForConfigOpts.podNamespace})
}

func startWaitForConfigManager(setupLog logr.Logger, config *rest.Config, podName types.NamespacedName) error {
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	defer cancel()

	setupLog.Info("Starting wait-for-config", "pod", podName)

	// Create a temporary client to set the annotation immediately
	tempClient, err := client.New(config, client.Options{})
	if err != nil {
		setupLog.Error(err, "failed to create kubernetes client")
		return err
	}

	// Set annotation on pod to signal that we are waiting for config
	setupLog.Info("Setting annotation on pod", "annotation", consts.DevicePluginWaitConfigAnnotation)
	err = setAnnotationOnPod(ctx, setupLog, tempClient, podName)
	if err != nil {
		setupLog.Error(err, "failed to set annotation on pod")
		return err
	}
	setupLog.Info("Annotation set successfully, waiting for removal")

	// Configure Manager
	// Watch only specific pod object
	selector := fields.SelectorFromSet(fields.Set{"metadata.name": podName.Name})
	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Metrics: metricsserver.Options{BindAddress: "0"},
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{podName.Namespace: {}},
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Pod{}: {Field: selector},
			},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return err
	}

	if err = (&WaitForConfigReconciler{
		Client: mgr.GetClient(),
		Pod:    podName,
		Cancel: cancel,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller")
		return err
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}
	return nil
}

// setAnnotationOnPod sets the wait-for-config annotation on the pod
func setAnnotationOnPod(ctx context.Context, logger logr.Logger, c client.Client, podName types.NamespacedName) error {
	return wait.ExponentialBackoff(wait.Backoff{
		Steps:    10,
		Duration: 1 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Cap:      30 * time.Second,
	}, func() (bool, error) {
		pod := &corev1.Pod{}
		if err := c.Get(ctx, podName, pod); err != nil {
			logger.Error(err, "failed to get pod, retrying")
			return false, nil
		}
		if err := utils.AnnotateObject(ctx, pod, consts.DevicePluginWaitConfigAnnotation, "true", c); err != nil {
			logger.Error(err, "failed to annotate pod, retrying")
			return false, nil
		}
		return true, nil
	})
}
