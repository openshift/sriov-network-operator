#!/usr/bin/env bash
set -xeo pipefail

OCP_VERSION=${OCP_VERSION:-4.18}
OCP_RELEASE_TYPE=${OCP_RELEASE_TYPE:-stable}
cluster_name=${CLUSTER_NAME:-ocp-virt}
domain_name=lab

api_ip=${API_IP:-192.168.123.253}
virtual_router_id=${VIRTUAL_ROUTER_ID:-253}
registry="default-route-openshift-image-registry.apps.${cluster_name}.${domain_name}"
HOME="/root"

NUM_OF_WORKERS=${NUM_OF_WORKERS:-3}
total_number_of_nodes=$((1 + NUM_OF_WORKERS))

if [ "$NUM_OF_WORKERS" -lt 3 ]; then
    echo "Min number of workers is 3"
    exit 1
fi

here="$(dirname "$(readlink --canonicalize "${BASH_SOURCE[0]}")")"
root="$(readlink --canonicalize "$here/..")"

source $here/run-e2e-conformance-common

check_requirements() {
  for cmd in kcli virsh podman make go jq base64 tar; do
    if ! command -v "$cmd" &> /dev/null; then
      echo "$cmd is not available"
      exit 1
    fi
  done
  return 0
}

echo "## checking requirements"
check_requirements
echo "## delete existing cluster name $cluster_name"
kcli delete cluster $cluster_name -y
kcli delete network $cluster_name -y

function cleanup {
  kcli delete cluster $cluster_name -y
  kcli delete network $cluster_name -y
}

if [ -z $SKIP_DELETE ]; then
  trap cleanup EXIT
fi

kcli create network -c 192.168.123.0/24 ocp
kcli create network -c 192.168.${virtual_router_id}.0/24 --nodhcp -i $cluster_name

cat <<EOF > ./${cluster_name}-plan.yaml
version: $OCP_RELEASE_TYPE
tag: $OCP_VERSION
ctlplane_memory: 32768
worker_memory: 8192
pool: default
disk_size: 50
network: ocp
api_ip: $api_ip
virtual_router_id: $virtual_router_id
domain: $domain_name
ctlplanes: 1
workers: $NUM_OF_WORKERS
machine: q35
network_type: OVNKubernetes
pull_secret: /root/openshift_pull.json
vmrules:
  - $cluster_name-worker-.*:
      nets:
        - name: ocp
          numa: 0
        - name: $cluster_name
          type: igb
          vfio: true
          noconf: true
          numa: 0
        - name: $cluster_name
          type: igb
          vfio: true
          noconf: true
          numa: 1
      numcpus: 6
      numa:
        - id: 0
          vcpus: 0,2,4
          memory: 4096
        - id: 1
          vcpus: 1,3,5
          memory: 4096

EOF

kcli create cluster openshift --paramfile ./${cluster_name}-plan.yaml $cluster_name

export KUBECONFIG=$HOME/.kcli/clusters/$cluster_name/auth/kubeconfig
export PATH=$PWD:$PATH

# w/a for the registry pull
kubectl create clusterrolebinding authenticated-registry-viewer --clusterrole registry-viewer --group system:unauthenticated

ATTEMPTS=0
MAX_ATTEMPTS=72
ready=false
sleep_time=10

until $ready || [ $ATTEMPTS -eq $MAX_ATTEMPTS ]
do
    echo "waiting for cluster to be ready"
    if [ `kubectl get node | grep Ready | wc -l` == $total_number_of_nodes ]; then
        echo "cluster is ready"
        ready=true
    else
        echo "cluster is not ready yet"
        sleep $sleep_time
    fi
    ATTEMPTS=$((ATTEMPTS+1))
done

if ! $ready; then
    echo "Timed out waiting for cluster to be ready"
    kubectl get nodes
    exit 1
fi

echo "## label cluster workers as sriov capable"
for ((num=0; num<NUM_OF_WORKERS; num++))
do
    kubectl label node $cluster_name-worker-$num.$domain_name feature.node.kubernetes.io/network-sriov.capable=true --overwrite
done

controller_ip=`kubectl get node -o wide | grep ctlp | awk '{print $6}'`

if [ `cat /etc/hosts | grep ${api_ip} | grep "default-route-openshift-image-registry.apps.${cluster_name}.${domain_name}" | wc -l` == 0 ]; then
  echo "adding registry to hosts"
  sed -i "s/${api_ip}/${api_ip} default-route-openshift-image-registry.apps.${cluster_name}.${domain_name}/g" /etc/hosts
fi


cat <<EOF | oc apply -f -
apiVersion: v1
kind: PersistentVolume
metadata:
  name: registry-pv
spec:
  capacity:
    storage: 60Gi
  volumeMode: Filesystem
  accessModes:
  - ReadWriteMany
  persistentVolumeReclaimPolicy: Delete
  storageClassName: registry-local-storage
  local:
    path: /mnt/
  nodeAffinity:
    required:
      nodeSelectorTerms:
      - matchExpressions:
        - key: kubernetes.io/hostname
          operator: In
          values:
          - ${cluster_name}-ctlplane-0.${domain_name}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: registry-pv-claim
  namespace: openshift-image-registry
spec:
  accessModes:
    - ReadWriteMany
  volumeMode: Filesystem
  resources:
    requests:
      storage: 60Gi
  storageClassName: registry-local-storage
EOF

kubectl patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":true,"storage":{"emptyDir": null,"pvc":{"claim":"registry-pv-claim"}},"topologySpreadConstraints":[],"rolloutStrategy":"Recreate","tolerations":[{"effect":"NoSchedule","key":"node-role.kubernetes.io/master","operator":"Exists"},{"effect":"NoSchedule","key":"node-role.kubernetes.io/control-plane","operator":"Exists"}]}}' --type=merge
kubectl patch ingresscontrollers.operator.openshift.io/default -n openshift-ingress-operator --patch '{"spec":{"replicas": 1}}' --type=merge

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

envsubst< deploy/namespace.yaml | kubectl apply -f -

echo "## build operator image"
podman build -t "${SRIOV_NETWORK_OPERATOR_IMAGE}" -f "${root}/Dockerfile" "${root}"

echo "## build daemon image"
podman build -t "${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}" -f "${root}/Dockerfile.sriov-network-config-daemon" "${root}"

echo "## build webhook image"
podman build -t "${SRIOV_NETWORK_WEBHOOK_IMAGE}" -f "${root}/Dockerfile.webhook" "${root}"

echo "## wait for the all cluster to be stable"
MAX_RETRIES=20
DELAY_SECONDS=10
retries=0
until [ $retries -ge $MAX_RETRIES ]; do
  # wait for all the openshift cluster operators to be running
  if [ $(kubectl get clusteroperator --no-headers | awk '{print $3}' | grep -v True | wc -l) -eq 0 ]; then
    break
  fi
  retries=$((retries+1))
  echo "cluster operators are not ready. Retrying... (Attempt $retries/$MAX_RETRIES)"
  sleep $DELAY_SECONDS
done

if [ $retries -eq $MAX_RETRIES ]; then
  echo "Max retries reached. Exiting..."
  exit 1
fi

echo "## wait for registry to be available"
kubectl wait configs.imageregistry.operator.openshift.io/cluster --for=condition=Available --timeout=120s

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

podman login -u serviceaccount -p ${pass} $registry --tls-verify=false

MAX_RETRIES=20
DELAY_SECONDS=10
retry_push() {
  local command="podman push --tls-verify=false $@"
  local retries=0

  until [ $retries -ge $MAX_RETRIES ]; do
    $command && break
    retries=$((retries+1))
    echo "Command failed. Retrying... (Attempt $retries/$MAX_RETRIES)"
    sleep $DELAY_SECONDS
  done

  if [ $retries -eq $MAX_RETRIES ]; then
    echo "Max retries reached. Exiting..."
    exit 1
  fi
}

retry_push "${SRIOV_NETWORK_OPERATOR_IMAGE}"
podman rmi -fi ${SRIOV_NETWORK_OPERATOR_IMAGE}
retry_push "${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}"
podman rmi -fi ${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}
retry_push "${SRIOV_NETWORK_WEBHOOK_IMAGE}"
podman rmi -fi ${SRIOV_NETWORK_WEBHOOK_IMAGE}

podman logout $registry

echo "## apply CRDs"
kubectl apply -f $root/config/crd/bases


cat <<EOF | kubectl apply -f -
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: ${NAMESPACE}
spec:
  disableDrain: false
  enableInjector: true
  enableOperatorWebhook: true
  logLevel: 2
EOF

export SRIOV_NETWORK_OPERATOR_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/sriov-network-operator:latest"
export SRIOV_NETWORK_CONFIG_DAEMON_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/sriov-network-config-daemon:latest"
export SRIOV_NETWORK_WEBHOOK_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/sriov-network-operator-webhook:latest"

if [[ -v LOCAL_SRIOV_CNI_IMAGE ]]; then
  podman_tag_and_push ${LOCAL_SRIOV_CNI_IMAGE} "$registry/$NAMESPACE/sriov-cni:latest"
  export SRIOV_CNI_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/sriov-cni:latest"
fi

if [[ -v LOCAL_SRIOV_DEVICE_PLUGIN_IMAGE ]]; then
  podman_tag_and_push ${LOCAL_SRIOV_DEVICE_PLUGIN_IMAGE} "$registry/$NAMESPACE/sriov-network-device-plugin:latest"
  export SRIOV_DEVICE_PLUGIN_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/sriov-network-device-plugin:latest"
fi

if [[ -v LOCAL_NETWORK_RESOURCES_INJECTOR_IMAGE ]]; then
  podman_tag_and_push ${LOCAL_NETWORK_RESOURCES_INJECTOR_IMAGE} "$registry/$NAMESPACE/network-resources-injector:latest"
  export NETWORK_RESOURCES_INJECTOR_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/network-resources-injector:latest"
fi

if [[ -v LOCAL_SRIOV_NETWORK_METRICS_EXPORTER_IMAGE ]]; then
  podman_tag_and_push ${LOCAL_SRIOV_NETWORK_METRICS_EXPORTER_IMAGE} "$registry/$NAMESPACE/sriov-network-metrics-exporter:latest"
  export METRICS_EXPORTER_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/sriov-network-metrics-exporter:latest"
fi

echo "## deploying SRIOV Network Operator"
hack/deploy-setup.sh $NAMESPACE

function cluster_info {
  if [[ -v TEST_REPORT_PATH ]]; then
    kubectl cluster-info dump --namespaces ${NAMESPACE},${MULTUS_NAMESPACE} --output-directory "${root}/${TEST_REPORT_PATH}/cluster-info"
  fi
}
trap cluster_info ERR

echo "## wait for sriov operator to be ready"
hack/deploy-wait.sh

if [ -z $SKIP_TEST ]; then
  echo "## run sriov e2e conformance tests"

  if [[ -v TEST_REPORT_PATH ]]; then
    export JUNIT_OUTPUT="${root}/${TEST_REPORT_PATH}/conformance-test-report"
  fi

  SUITE=./test/conformance hack/run-e2e-conformance.sh
fi
