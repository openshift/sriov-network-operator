package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// SriovNetworkPoolConfigReconciler reconciles a SriovNetworkPoolConfig object
type SriovNetworkPoolConfigReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	PlatformHelper platforms.Interface
}

const (
	mcPriority string = "00-"
)

//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovnetworkpoolconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovnetworkpoolconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=sriovnetwork.openshift.io,resources=sriovnetworkpoolconfigs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SriovNetworkPoolConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.6.4/pkg/reconcile
func (r *SriovNetworkPoolConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("sriovnetworkpoolconfig", req.NamespacedName)
	isHypershift := false
	if r.PlatformHelper.IsOpenshiftCluster() {
		if r.PlatformHelper.IsHypershift() {
			isHypershift = true
		}
		logger = logger.WithValues("isHypershift", isHypershift)
	}
	logger.Info("Reconciling")

	// Fetch SriovNetworkPoolConfig
	instance := &sriovnetworkv1.SriovNetworkPoolConfig{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			logger.Info("instance not found")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// we don't need a finalizer for pools that doesn't use the ovs hardware offload feature
	if instance.Spec.OvsHardwareOffloadConfig.Name == "" {
		return ctrl.Result{}, nil
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !sriovnetworkv1.StringInArray(sriovnetworkv1.POOLCONFIGFINALIZERNAME, instance.ObjectMeta.Finalizers) {
			instance.ObjectMeta.Finalizers = append(instance.ObjectMeta.Finalizers, sriovnetworkv1.POOLCONFIGFINALIZERNAME)
			if err := r.Update(ctx, instance); err != nil {
				return reconcile.Result{}, err
			}
		}
		if vars.ClusterType == constants.ClusterTypeOpenshift {
			if !isHypershift {
				if err = r.syncOvsHardwareOffloadMachineConfigs(ctx, instance, false); err != nil {
					return reconcile.Result{}, err
				}
			} else {
				logger.Info("Ignoring request to enable HWOL (running on Hypershift)")
			}
		}
	} else {
		// The object is being deleted
		if sriovnetworkv1.StringInArray(sriovnetworkv1.POOLCONFIGFINALIZERNAME, instance.ObjectMeta.Finalizers) {
			// our finalizer is present, so lets handle any external dependency
			logger.Info("delete SriovNetworkPoolConfig CR", "Namespace", instance.Namespace, "Name", instance.Name)
			if vars.ClusterType == constants.ClusterTypeOpenshift && !isHypershift {
				if err = r.syncOvsHardwareOffloadMachineConfigs(ctx, instance, true); err != nil {
					// if fail to delete the external dependency here, return with error
					// so that it can be retried
					return reconcile.Result{}, err
				}
			}
			// remove our finalizer from the list and update it.
			var found bool
			instance.ObjectMeta.Finalizers, found = sriovnetworkv1.RemoveString(sriovnetworkv1.POOLCONFIGFINALIZERNAME, instance.ObjectMeta.Finalizers)
			if found {
				if err := r.Update(ctx, instance); err != nil {
					return reconcile.Result{}, err
				}
			}
		}
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: constants.ResyncPeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SriovNetworkPoolConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sriovnetworkv1.SriovNetworkPoolConfig{}).
		Complete(r)
}

func (r *SriovNetworkPoolConfigReconciler) syncOvsHardwareOffloadMachineConfigs(ctx context.Context, nc *sriovnetworkv1.SriovNetworkPoolConfig, deletion bool) error {
	logger := log.Log.WithName("syncOvsHardwareOffloadMachineConfigs")

	mcpName := nc.Spec.OvsHardwareOffloadConfig.Name
	mcName := mcPriority + mcpName + "-" + constants.OVSHWOLMachineConfigNameSuffix

	foundMC := &mcfgv1.MachineConfig{}
	mcp := &mcfgv1.MachineConfigPool{}

	if mcpName == "" {
		logger.Info("MachineConfigPool name is not defined in SriovNetworkPoolConfig", nc.GetNamespace(), nc.GetName())
		return nil
	}

	if mcpName == "master" {
		logger.Info("Master nodes are selected by OvsHardwareOffloadConfig.Name, ignoring.")
		return nil
	}

	err := r.Get(ctx, types.NamespacedName{Name: mcpName}, mcp)
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("machineConfigPool %s doesn't exist: %v", mcpName, err)
		}
	}

	data := render.MakeRenderData()
	mc, err := render.GenerateMachineConfig("bindata/manifests/switchdev-config", mcName, mcpName, true, &data)
	if err != nil {
		return err
	}

	err = r.Get(ctx, types.NamespacedName{Name: mcName}, foundMC)
	if err != nil {
		if errors.IsNotFound(err) {
			if deletion {
				logger.Info("MachineConfig has already been deleted")
			} else {
				err = r.Create(ctx, mc)
				if err != nil {
					return fmt.Errorf("couldn't create MachineConfig: %v", err)
				}
				logger.Info("Created MachineConfig CR in MachineConfigPool", mcName, mcpName)
			}
		} else {
			return fmt.Errorf("failed to get MachineConfig: %v", err)
		}
	} else {
		if deletion {
			logger.Info("offload disabled, delete MachineConfig")
			err = r.Delete(ctx, foundMC)
			if err != nil {
				return fmt.Errorf("couldn't delete MachineConfig: %v", err)
			}
		} else {
			var foundIgn, renderedIgn interface{}
			// The Raw config JSON string may have the fields reordered.
			// For example the "path" field may come before the "contents"
			// field in the rendered ignition JSON; while the found
			// MachineConfig's ignition JSON would have it the other way around.
			// Thus we need to unmarshal the JSON for both found and rendered
			// ignition and compare.
			json.Unmarshal(foundMC.Spec.Config.Raw, &foundIgn)
			json.Unmarshal(mc.Spec.Config.Raw, &renderedIgn)
			if !reflect.DeepEqual(foundIgn, renderedIgn) {
				logger.Info("MachineConfig already exists, updating")
				mc.SetResourceVersion(foundMC.GetResourceVersion())
				err = r.Update(ctx, mc)
				if err != nil {
					return fmt.Errorf("couldn't update MachineConfig: %v", err)
				}
			} else {
				logger.Info("No content change, skip updating MachineConfig")
			}
		}
	}

	return nil
}
