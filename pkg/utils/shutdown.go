package utils

import (
	"context"

	admv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	conf "sigs.k8s.io/controller-runtime/pkg/client/config"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var shutdownLog = ctrl.Log.WithName("shutdown")

var failurePolicyIgnore = admv1.Ignore

func Shutdown(c client.Client) {
	updateFinalizers(c)
	updateWebhooks()
}

func updateFinalizers(c client.Client) {
	shutdownLog.Info("Clearing finalizers on exit")
	networkList := &sriovnetworkv1.SriovNetworkList{}
	err := c.List(context.TODO(), networkList, &client.ListOptions{Namespace: vars.Namespace})
	if err != nil {
		shutdownLog.Error(err, "Failed to list SriovNetworks")
		return
	}

	for _, instance := range networkList.Items {
		if len(instance.ObjectMeta.Finalizers) == 0 {
			continue
		}
		if err != nil {
			shutdownLog.Error(err, "Failed get finalizers map")
		}
		shutdownLog.Info("Clearing finalizers on SriovNetwork ", "namespace", instance.GetNamespace(), "name", instance.GetName())
		var found bool
		instance.ObjectMeta.Finalizers, found = sriovnetworkv1.RemoveString(sriovnetworkv1.NETATTDEFFINALIZERNAME, instance.ObjectMeta.Finalizers)
		if found {
			err = c.Update(context.TODO(), &instance)
			if err != nil {
				shutdownLog.Error(err, "Failed to remove finalizer")
			}
		}
	}

	shutdownLog.Info("Done clearing finalizers on exit")
}

func updateWebhooks() {
	shutdownLog.Info("Seting webhook failure policies to Ignore on exit")
	clientset, err := kubernetes.NewForConfig(conf.GetConfigOrDie())
	if err != nil {
		shutdownLog.Error(err, "Error getting client")
	}
	updateValidatingWebhook(clientset)
	updateMutatingWebhooks(clientset)
	shutdownLog.Info("Done seting webhook failure policies to Ignore")
}

func updateValidatingWebhook(c *kubernetes.Clientset) {
	validatingWebhookClient := c.AdmissionregistrationV1().ValidatingWebhookConfigurations()
	webhook, err := validatingWebhookClient.Get(context.TODO(), consts.OperatorWebHookName, metav1.GetOptions{})
	if err != nil {
		shutdownLog.Error(err, "Error getting webhook")
		return
	}
	webhook.Webhooks[0].FailurePolicy = &failurePolicyIgnore
	_, err = validatingWebhookClient.Update(context.TODO(), webhook, metav1.UpdateOptions{})
	if err != nil {
		shutdownLog.Error(err, "Error updating webhook")
	}
}

func updateMutatingWebhooks(c *kubernetes.Clientset) {
	mutatingWebhookClient := c.AdmissionregistrationV1().MutatingWebhookConfigurations()
	for _, name := range []string{consts.OperatorWebHookName, consts.InjectorWebHookName} {
		mutatingWebhook, err := mutatingWebhookClient.Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			shutdownLog.Error(err, "Error getting webhook")
			continue
		}
		mutatingWebhook.Webhooks[0].FailurePolicy = &failurePolicyIgnore
		_, err = mutatingWebhookClient.Update(context.TODO(), mutatingWebhook, metav1.UpdateOptions{})
		if err != nil {
			shutdownLog.Error(err, "Error updating webhook")
		}
	}
}
