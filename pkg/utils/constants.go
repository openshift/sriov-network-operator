package utils

import "time"

const (
	ResyncPeriod                   = 5 * time.Minute
	DefaultConfigName              = "default"
	ConfigDaemonPath               = "./bindata/manifests/daemon"
	InjectorWebHookPath            = "./bindata/manifests/webhook"
	OperatorWebHookPath            = "./bindata/manifests/operator-webhook"
	ServiceCAConfigMapAnnotation   = "service.beta.openshift.io/inject-cabundle"
	InjectorWebHookName            = "network-resources-injector-config"
	OperatorWebHookName            = "sriov-operator-webhook-config"
	DeprecatedOperatorWebHookName  = "operator-webhook-config"
	PluginPath                     = "./bindata/manifests/plugins"
	DaemonPath                     = "./bindata/manifests/daemon"
	DefaultPolicyName              = "default"
	ConfigMapName                  = "device-plugin-config"
	DPConfigFileName               = "config.json"
	OVSHWOLMachineConfigNameSuffix = "ovs-hw-offload"

	LinkTypeEthernet   = "ether"
	LinkTypeInfiniband = "infiniband"
)
