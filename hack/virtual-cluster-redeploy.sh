#!/usr/bin/env bash
set -xeo pipefail

HOME="/root"
here="$(dirname "$(readlink --canonicalize "${BASH_SOURCE[0]}")")"
root="$(readlink --canonicalize "$here/..")"
domain_name=lab

if [ $CLUSTER_TYPE == "openshift" ]; then
  echo "Openshift"
  cluster_name=${CLUSTER_NAME:-ocp-virt}
  export NAMESPACE="openshift-sriov-network-operator"
  export KUBECONFIG=/root/.kcli/clusters/$cluster_name/auth/kubeconfig

  dockercgf=`kubectl -n ${NAMESPACE} get sa builder -oyaml | grep imagePullSecrets -A 1 | grep -o "builder-.*"`
  auth=`kubectl -n ${NAMESPACE} get secret ${dockercgf} -ojson | jq '.data.".dockercfg"'`
  auth="${auth:1:-1}"
  auth=`echo ${auth} | base64 -d`
  echo ${auth} > registry-login.conf

  internal_registry="image-registry.openshift-image-registry.svc:5000"
  pass=$( jq .\"image-registry.openshift-image-registry.svc:5000\".auth registry-login.conf  )
  pass=`echo ${pass:1:-1} | base64 -d`

  # dockercfg password is in the form `<token>:password`. We need to trim the `<token>:` prefix
  pass=${pass#"<token>:"}

  registry=`kubectl -n openshift-image-registry get route --no-headers | awk '{print $2}'`
  podman login -u serviceaccount -p ${pass} $registry --tls-verify=false

  export ADMISSION_CONTROLLERS_ENABLED=true
  export SKIP_VAR_SET=""
  export NAMESPACE="openshift-sriov-network-operator"
  export OPERATOR_NAMESPACE=$NAMESPACE
  export MULTUS_NAMESPACE="openshift-multus"
  export OPERATOR_EXEC=kubectl
  export CLUSTER_TYPE=openshift
  export DEV_MODE=TRUE
  export CLUSTER_HAS_EMULATED_PF=TRUE
  export METRICS_EXPORTER_PROMETHEUS_OPERATOR_ENABLED=true
  export METRICS_EXPORTER_PROMETHEUS_DEPLOY_RULES=true
  export METRICS_EXPORTER_PROMETHEUS_OPERATOR_SERVICE_ACCOUNT=${METRICS_EXPORTER_PROMETHEUS_OPERATOR_SERVICE_ACCOUNT:-"prometheus-k8s"}
  export METRICS_EXPORTER_PROMETHEUS_OPERATOR_NAMESPACE=${METRICS_EXPORTER_PROMETHEUS_OPERATOR_NAMESPACE:-"openshift-monitoring"}

  export SRIOV_NETWORK_OPERATOR_IMAGE="$registry/$NAMESPACE/sriov-network-operator:latest"
  export SRIOV_NETWORK_CONFIG_DAEMON_IMAGE="$registry/$NAMESPACE/sriov-network-config-daemon:latest"
  export SRIOV_NETWORK_WEBHOOK_IMAGE="$registry/$NAMESPACE/sriov-network-operator-webhook:latest"
else
  echo "K8S"
  cluster_name=${CLUSTER_NAME:-virtual}
  export NAMESPACE="sriov-network-operator"
  export KUBECONFIG=/root/.kcli/clusters/$cluster_name/auth/kubeconfig

  controller_ip=`kubectl get node -o wide | grep ctlp | awk '{print $6}'`

  export SRIOV_NETWORK_OPERATOR_IMAGE="$controller_ip:5000/sriov-network-operator:latest"
  export SRIOV_NETWORK_CONFIG_DAEMON_IMAGE="$controller_ip:5000/sriov-network-config-daemon:latest"
  export SRIOV_NETWORK_WEBHOOK_IMAGE="$controller_ip:5000/sriov-network-operator-webhook:latest"

  export ADMISSION_CONTROLLERS_CERTIFICATES_CERT_MANAGER_ENABLED=true
  export CNI_BIN_PATH=/opt/cni/bin
fi

export ADMISSION_CONTROLLERS_ENABLED=true
export SKIP_VAR_SET=""
export OPERATOR_NAMESPACE=$NAMESPACE
export OPERATOR_EXEC=kubectl
export DEV_MODE=TRUE
export CLUSTER_HAS_EMULATED_PF=TRUE

echo "## build operator image"
podman build -t "${SRIOV_NETWORK_OPERATOR_IMAGE}" -f "${root}/Dockerfile" "${root}"

echo "## build daemon image"
podman build -t "${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}" -f "${root}/Dockerfile.sriov-network-config-daemon" "${root}"

echo "## build webhook image"
podman build -t "${SRIOV_NETWORK_WEBHOOK_IMAGE}" -f "${root}/Dockerfile.webhook" "${root}"

podman push --tls-verify=false "${SRIOV_NETWORK_OPERATOR_IMAGE}"
podman push --tls-verify=false "${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}"
podman push --tls-verify=false "${SRIOV_NETWORK_WEBHOOK_IMAGE}"

if [ $CLUSTER_TYPE == "openshift" ]; then
  export SRIOV_NETWORK_OPERATOR_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/sriov-network-operator:latest"
  export SRIOV_NETWORK_CONFIG_DAEMON_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/sriov-network-config-daemon:latest"
  export SRIOV_NETWORK_WEBHOOK_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/sriov-network-operator-webhook:latest"

  echo "## apply CRDs"
  kubectl apply -k $root/config/crd

  echo "## deploying SRIOV Network Operator"
  hack/deploy-setup.sh $NAMESPACE
else
  export HELM_MODE=upgrade
  hack/deploy-operator-helm.sh
fi

kubectl -n ${NAMESPACE} delete po --all
