#!/usr/bin/env bash
set -xeo pipefail

cluster_name=${CLUSTER_NAME:-virtual}
domain_name=$cluster_name.lab

api_ip=${API_IP:-192.168.124.250}
virtual_router_id=${VIRTUAL_ROUTER_ID:-250}
HOME="/root"

here="$(dirname "$(readlink --canonicalize "${BASH_SOURCE[0]}")")"
root="$(readlink --canonicalize "$here/..")"

NUM_OF_WORKERS=${NUM_OF_WORKERS:-2}
total_number_of_nodes=$((1 + NUM_OF_WORKERS))

if [ "$NUM_OF_WORKERS" -lt 2 ]; then
    echo "Min number of workers is 2"
    exit 1
fi

check_requirements() {
  for cmd in kcli virsh virt-edit podman make go; do
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

kcli create network -c 192.168.124.0/24 k8s
kcli create network -c 192.168.${virtual_router_id}.0/24 --nodhcp -i $cluster_name

cat <<EOF > ./${cluster_name}-plan.yaml
ctlplane_memory: 4096
worker_memory: 4096
pool: default
disk_size: 50
network: k8s
api_ip: $api_ip
virtual_router_id: $virtual_router_id
domain: $domain_name
ctlplanes: 1
workers: $NUM_OF_WORKERS
ingress: false
machine: q35
engine: crio
sdn: flannel
autolabeller: false
vmrules:
  - $cluster_name-worker-.*:
      nets:
        - name: k8s
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
          memory: 2048
        - id: 1
          vcpus: 1,3,5
          memory: 2048

EOF

kcli create cluster generic --paramfile ./${cluster_name}-plan.yaml $cluster_name

export KUBECONFIG=$HOME/.kcli/clusters/$cluster_name/auth/kubeconfig
export PATH=$PWD:$PATH

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

function update_worker_labels() {
echo "## label cluster workers as sriov capable"
for ((num=0; num<NUM_OF_WORKERS; num++))
do
    kubectl label node $cluster_name-worker-$num.$domain_name feature.node.kubernetes.io/network-sriov.capable=true --overwrite
done

echo "## label cluster worker as worker"
for ((num=0; num<NUM_OF_WORKERS; num++))
do
  kubectl label node $cluster_name-worker-$num.$domain_name node-role.kubernetes.io/worker= --overwrite
done
}

update_worker_labels

controller_ip=`kubectl get node -o wide | grep ctlp | awk '{print $6}'`
insecure_registry="[[registry]]
location = \"$controller_ip:5000\"
insecure = true

[aliases]
\"golang\" = \"docker.io/library/golang\"
"

cat << EOF > /etc/containers/registries.conf.d/003-${cluster_name}.conf
$insecure_registry
EOF

function update_host() {
    node_name=$1
    kcli ssh $node_name << EOF
sudo su
echo '$insecure_registry' > /etc/containers/registries.conf.d/003-internal.conf
systemctl restart crio

echo '[connection]
id=multi
type=ethernet
[ethernet]
[match]
driver=igbvf;
[ipv4]
method=disabled
[ipv6]
addr-gen-mode=default
method=disabled
[proxy]' > /etc/NetworkManager/system-connections/multi.nmconnection

chmod 600 /etc/NetworkManager/system-connections/multi.nmconnection
systemctl restart NetworkManager

EOF

}

update_host $cluster_name-ctlplane-0
for ((num=0; num<NUM_OF_WORKERS; num++))
do
  update_host $cluster_name-worker-$num
done

kubectl create namespace container-registry

echo "## deploy internal registry"
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: PersistentVolume
metadata:
  name: registry-pv
spec:
  capacity:
    storage: 60Gi
  volumeMode: Filesystem
  accessModes:
  - ReadWriteOnce
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
EOF

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: registry-pv-claim
  namespace: container-registry
spec:
  accessModes:
    - ReadWriteOnce
  volumeMode: Filesystem
  resources:
    requests:
      storage: 60Gi
  storageClassName: registry-local-storage
EOF

cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: registry
  namespace: container-registry
spec:
  replicas: 1
  selector:
    matchLabels:
      app: registry
  template:
    metadata:
      labels:
        app: registry
    spec:
      hostNetwork: true
      tolerations:
        - effect: NoSchedule
          key: node-role.kubernetes.io/control-plane
      containers:
      - image: quay.io/libpod/registry:2.8.2
        imagePullPolicy: Always
        name: registry
        volumeMounts:
        - name: data
          mountPath: /var/lib/registry
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: registry-pv-claim
      terminationGracePeriodSeconds: 10
EOF


export SRIOV_NETWORK_OPERATOR_IMAGE="$controller_ip:5000/sriov-network-operator:latest"
export SRIOV_NETWORK_CONFIG_DAEMON_IMAGE="$controller_ip:5000/sriov-network-config-daemon:latest"
export SRIOV_NETWORK_WEBHOOK_IMAGE="$controller_ip:5000/sriov-network-operator-webhook:latest"

echo "## build operator image"
podman build -t "${SRIOV_NETWORK_OPERATOR_IMAGE}" -f "${root}/Dockerfile" "${root}"

echo "## build daemon image"
podman build -t "${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}" -f "${root}/Dockerfile.sriov-network-config-daemon" "${root}"

echo "## build webhook image"
podman build -t "${SRIOV_NETWORK_WEBHOOK_IMAGE}" -f "${root}/Dockerfile.webhook" "${root}"

podman push --tls-verify=false "${SRIOV_NETWORK_OPERATOR_IMAGE}"
podman push --tls-verify=false "${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}"
podman push --tls-verify=false "${SRIOV_NETWORK_WEBHOOK_IMAGE}"

# remove the crio bridge and let flannel to recreate
kcli ssh $cluster_name-ctlplane-0 << EOF
sudo su
if [ $(ip a | grep 10.85.0 | wc -l) -eq 0 ]; then ip link del cni0; fi
EOF


kubectl -n kube-system get po | grep multus | awk '{print "kubectl -n kube-system delete po",$1}' | sh
kubectl -n kube-system get po | grep coredns | awk '{print "kubectl -n kube-system delete po",$1}' | sh

TIMEOUT=400
echo "## wait for coredns"
kubectl -n kube-system wait --for=condition=available deploy/coredns --timeout=${TIMEOUT}s
echo "## wait for multus"
kubectl -n kube-system wait --for=condition=ready -l name=multus pod --timeout=${TIMEOUT}s

echo "## deploy cert manager"
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.12.0/cert-manager.yaml

echo "## wait for cert manager to be ready"

ATTEMPTS=0
MAX_ATTEMPTS=72
ready=false
sleep_time=5

until $ready || [ $ATTEMPTS -eq $MAX_ATTEMPTS ]
do
    echo "waiting for cert manager webhook to be ready"
    if [ `kubectl -n cert-manager get po | grep webhook | grep "1/1" | wc -l` == 1 ]; then
        echo "cluster is ready"
        ready=true
    else
        echo "cert manager webhook is not ready yet"
        sleep $sleep_time
    fi
    ATTEMPTS=$((ATTEMPTS+1))
done


export ADMISSION_CONTROLLERS__ENABLED=true
export ADMISSION_CONTROLLERS__CERTIFICATES__CERT_MANAGER__ENABLED=true
export SKIP_VAR_SET=""
export NAMESPACE="sriov-network-operator"
export OPERATOR_NAMESPACE="sriov-network-operator"
export CNI_BIN_PATH=/opt/cni/bin
export OPERATOR_EXEC=kubectl
export CLUSTER_TYPE=kubernetes
export DEV_MODE=TRUE
export CLUSTER_HAS_EMULATED_PF=TRUE

echo "## deploy namespace"
envsubst< $root/deploy/namespace.yaml | ${OPERATOR_EXEC} apply -f -

echo "## create certificates for webhook"
cat <<EOF | kubectl apply -f -
---
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: selfsigned-issuer
  namespace: ${NAMESPACE}
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: network-resources-injector-cert
  namespace: ${NAMESPACE}
spec:
  commonName: network-resources-injector-service.svc
  dnsNames:
  - network-resources-injector-service.${NAMESPACE}.svc.cluster.local
  - network-resources-injector-service.${NAMESPACE}.svc
  issuerRef:
    kind: Issuer
    name: selfsigned-issuer
  secretName: network-resources-injector-cert
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: operator-webhook-cert
  namespace: ${NAMESPACE}
spec:
  commonName: operator-webhook-service.svc
  dnsNames:
  - operator-webhook-service.${NAMESPACE}.svc.cluster.local
  - operator-webhook-service.${NAMESPACE}.svc
  issuerRef:
    kind: Issuer
    name: selfsigned-issuer
  secretName: operator-webhook-cert
EOF


echo "## apply CRDs"
kubectl apply -k $root/config/crd

echo "## deploying SRIOV Network Operator"
hack/deploy-setup.sh $NAMESPACE

echo "## wait for sriov operator to be ready"
hack/deploy-wait.sh

if [ -z $SKIP_TEST ]; then
  echo "## run sriov e2e conformance tests"

  if [[ -v TEST_REPORT_PATH ]]; then
    export JUNIT_OUTPUT="${root}/${TEST_REPORT_PATH}/conformance-test-report"
  fi

  SUITE=./test/conformance hack/run-e2e-conformance.sh

  if [[ -v TEST_REPORT_PATH ]]; then
    kubectl cluster-info dump --namespaces ${NAMESPACE} --output-directory "${root}/${TEST_REPORT_PATH}/cluster-info"
  fi
fi
