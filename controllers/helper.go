/*


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

package controllers

import (
	"os"
	"time"
)

const (
	ResyncPeriod                    = 5 * time.Minute
	DEFAULT_CONFIG_NAME             = "default"
	CONFIG_DAEMON_PATH              = "./bindata/manifests/daemon"
	INJECTOR_WEBHOOK_PATH           = "./bindata/manifests/webhook"
	OPERATOR_WEBHOOK_PATH           = "./bindata/manifests/operator-webhook"
	INJECTOR_SERVICE_CA_CONFIGMAP   = "injector-service-ca"
	WEBHOOK_SERVICE_CA_CONFIGMAP    = "webhook-service-ca"
	SERVICE_CA_CONFIGMAP_ANNOTATION = "service.beta.openshift.io/inject-cabundle"
	INJECTOR_WEBHOOK_NAME           = "network-resources-injector-config"
	OPERATOR_WEBHOOK_NAME           = "sriov-operator-webhook-config"
	PLUGIN_PATH                     = "./bindata/manifests/plugins"
	DAEMON_PATH                     = "./bindata/manifests/daemon"
	DEFAULT_POLICY_NAME             = "default"
	CONFIGMAP_NAME                  = "device-plugin-config"
	DP_CONFIG_FILENAME              = "config.json"
)

var webhooks = map[string](string){
	INJECTOR_WEBHOOK_NAME: INJECTOR_WEBHOOK_PATH,
	OPERATOR_WEBHOOK_NAME: OPERATOR_WEBHOOK_PATH,
}

var namespace = os.Getenv("NAMESPACE")
