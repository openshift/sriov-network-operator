package utils

import "time"

const (
	ResyncPeriod                     = 5 * time.Minute
	DEFAULT_CONFIG_NAME              = "default"
	CONFIG_DAEMON_PATH               = "./bindata/manifests/daemon"
	INJECTOR_WEBHOOK_PATH            = "./bindata/manifests/webhook"
	OPERATOR_WEBHOOK_PATH            = "./bindata/manifests/operator-webhook"
	SERVICE_CA_CONFIGMAP_ANNOTATION  = "service.beta.openshift.io/inject-cabundle"
	INJECTOR_WEBHOOK_NAME            = "network-resources-injector-config"
	OPERATOR_WEBHOOK_NAME            = "sriov-operator-webhook-config"
	DEPRECATED_OPERATOR_WEBHOOK_NAME = "operator-webhook-config"
	PLUGIN_PATH                      = "./bindata/manifests/plugins"
	DAEMON_PATH                      = "./bindata/manifests/daemon"
	DEFAULT_POLICY_NAME              = "default"
	CONFIGMAP_NAME                   = "device-plugin-config"
	DP_CONFIG_FILENAME               = "config.json"
	HwOffloadNodeLabel               = "ovs-hw-offload-worker"

	LinkTypeEthernet   = "ether"
	LinkTypeInfiniband = "infiniband"
)
