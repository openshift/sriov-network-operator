package k8sreporter

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

func SriovNetworkNodeStatesSummary(reader client.Reader) string {
	ret := "SriovNetworkNodeStates:\n"
	nodeStates := &sriovv1.SriovNetworkNodeStateList{}
	err := reader.List(context.Background(), nodeStates, &client.ListOptions{})
	if err != nil {
		return ret + "Summary error: " + err.Error()
	}

	for _, state := range nodeStates.Items {
		ret += fmt.Sprintf("%s\t%s\t%+v\n", state.Name, state.Status.SyncStatus, state.Annotations)
	}

	return ret
}

func Events(reader client.Reader, namespace string) string {
	ret := fmt.Sprintf("Events in [%s]:\n", namespace)
	events := &corev1.EventList{}
	err := reader.List(context.Background(), events, &client.ListOptions{})
	if err != nil {
		return ret + fmt.Sprintf("can't retrieve events for namespace %s: %s", namespace, err.Error())
	}

	for _, item := range events.Items {
		ret += fmt.Sprintf("%s: %s\t%s\t%s\n", item.LastTimestamp, item.Reason, item.InvolvedObject.Name, item.Message)
	}

	return ret
}
