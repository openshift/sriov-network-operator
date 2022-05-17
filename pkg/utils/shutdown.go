package utils

import (
	"context"

	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	admv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	conf "sigs.k8s.io/controller-runtime/pkg/client/config"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

var shutdownLog = ctrl.Log.WithName("shutdown")

var failurePolicyIgnore = admv1.Ignore

func Shutdown() {
	updateFinalizers()
	updateWebhooks()
}

func updateFinalizers() {
	shutdownLog.Info("Clearing finalizers on exit")
	c, err := snclientset.NewForConfig(conf.GetConfigOrDie())
	if err != nil {
		shutdownLog.Error(err, "Error creating client")
	}
	sriovNetworkClient := c.SriovnetworkV1()
	networkList, err := sriovNetworkClient.SriovNetworks("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		shutdownLog.Error(err, "Failed to list SriovNetworks")
	} else {
		for _, instance := range networkList.Items {
			if instance.ObjectMeta.Finalizers == nil || len(instance.ObjectMeta.Finalizers) == 0 {
				continue
			}
			if err != nil {
				shutdownLog.Error(err, "Failed get finalizers map")
			}
			shutdownLog.Info("Clearing finalizers on SriovNetwork ", "namespace", instance.GetNamespace(), "name", instance.GetName())
			var found bool
			instance.ObjectMeta.Finalizers, found = sriovnetworkv1.RemoveString(sriovnetworkv1.NETATTDEFFINALIZERNAME, instance.ObjectMeta.Finalizers)
			if found {
				_, err = sriovNetworkClient.SriovNetworks(instance.GetNamespace()).Update(context.TODO(), &instance, metav1.UpdateOptions{})
				if err != nil {
					shutdownLog.Error(err, "Failed to remove finalizer")
				}
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
	webhook, err := validatingWebhookClient.Get(context.TODO(), OPERATOR_WEBHOOK_NAME, metav1.GetOptions{})
	if err != nil {
		shutdownLog.Error(err, "Error getting webhook")
	}
	webhook.Webhooks[0].FailurePolicy = &failurePolicyIgnore
	_, err = validatingWebhookClient.Update(context.TODO(), webhook, metav1.UpdateOptions{})
	if err != nil {
		shutdownLog.Error(err, "Error updating webhook")
	}
}

func updateMutatingWebhooks(c *kubernetes.Clientset) {
	mutatingWebhookClient := c.AdmissionregistrationV1().MutatingWebhookConfigurations()
	for _, name := range []string{OPERATOR_WEBHOOK_NAME, INJECTOR_WEBHOOK_NAME} {
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
