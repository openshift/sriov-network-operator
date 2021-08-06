package utils

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

func Shutdown(c client.Client) {
	shutdownLog := ctrl.Log.WithName("shutdown")
	shutdownLog.Info("Clearing finalizers on exit")
	networkList := &sriovnetworkv1.SriovNetworkList{}
	err := c.List(context.TODO(), networkList)
	if err != nil {
		shutdownLog.Error(err, "Failed to list SriovNetworks")
	} else {
		for _, instance := range networkList.Items {
			shutdownLog.Info("Clearing finalizers on SriovNetwork ", "namespace", instance.GetNamespace(), "name", instance.GetName())
			instance.ObjectMeta.Finalizers = sriovnetworkv1.RemoveString(sriovnetworkv1.FINALIZERNAME, instance.ObjectMeta.Finalizers)
			err := c.Update(context.TODO(), &instance)
			if err != nil {
				shutdownLog.Error(err, "Failed to remove finalizer")
			}
		}
	}
	shutdownLog.Info("Done clearing finalizers on exit")
}
