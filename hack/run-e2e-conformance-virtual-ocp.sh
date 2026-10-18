#!/usr/bin/env bash
set -xeo pipefail

OCP_VERSION=${OCP_VERSION:-4.22.3}
OCP_RELEASE_TYPE=${OCP_RELEASE_TYPE:-stable}
cluster_name=${CLUSTER_NAME:-ocp-virt}
domain_name=lab

api_ip=${API_IP:-192.168.123.253}
virtual_router_id=${VIRTUAL_ROUTER_ID:-253}
registry="default-route-openshift-image-registry.apps.${cluster_name}.${domain_name}"

NUM_OF_WORKERS=${NUM_OF_WORKERS:-3}
total_number_of_nodes=$((1 + NUM_OF_WORKERS))

if [ "$NUM_OF_WORKERS" -lt 3 ] || [ "$NUM_OF_WORKERS" -gt 256 ]; then
    echo "Number of workers must be between 3 and 256"
    exit 1
fi

here="$(dirname "$(readlink --canonicalize "${BASH_SOURCE[0]}")")"
root="$(readlink --canonicalize "$here/..")"

source "$here/run-e2e-conformance-common"

MOCK_SMARTNIC_REPO=${MOCK_SMARTNIC_REPO:-https://github.com/SchSeba/switchdev-mock-driver.git}
validate_mock_driver_repo "$MOCK_SMARTNIC_REPO"
export MOCK_DRIVER_WORKDIR
MOCK_DRIVER_WORKDIR=$(mktemp -d /tmp/mock-smartnic-ocp.XXXXXX)
export MOCK_DRIVER_SOURCE=${MOCK_DRIVER_SOURCE:-$root/../switchdev-mock-driver}
if [[ ! -d $MOCK_DRIVER_SOURCE ]]; then
  export MOCK_DRIVER_SOURCE=$MOCK_DRIVER_WORKDIR/source
fi
fetch_mock_driver_source "$MOCK_SMARTNIC_REPO" "$MOCK_DRIVER_SOURCE"

check_requirements() {
  for cmd in kcli virsh podman make go jq base64 tar oc kubectl python3 timeout git; do
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
  sudo rm -f /etc/containers/registries.conf.d/003-${cluster_name}.conf
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
pull_secret: $HOME/openshift_pull.json
vmrules:
EOF

# Third NIC is the mock PF; retain two native test PFs beside management.
for ((num=0; num<NUM_OF_WORKERS; num++)); do
  printf -v native_mac '52:54:00:fb:%02x:02' "$num"
  printf -v mock_mac '52:54:00:fb:%02x:03' "$num"
  printf -v extra_mac '52:54:00:fb:%02x:04' "$num"
  cat <<EOF >> ./${cluster_name}-plan.yaml
  - $cluster_name-worker-$num$:
      nets:
        - name: ocp
          numa: 0
        - name: $cluster_name
          type: igb
          mac: $native_mac
          vfio: true
          noconf: true
          numa: 0
        - name: $cluster_name
          type: igb
          mac: $mock_mac
          vfio: true
          noconf: true
          numa: 1
        - name: $cluster_name
          type: igb
          mac: $extra_mac
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
done

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
    if kubectl get nodes -o json | jq -e --argjson count "$total_number_of_nodes" '
      (.items | length) == $count and all(.items[]; any(.status.conditions[]; .type == "Ready" and .status == "True"))' >/dev/null; then
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

if ! grep -qF "$registry" /etc/hosts; then
  echo "adding registry to hosts"
  sudo sed -i "s/${api_ip}/${api_ip} default-route-openshift-image-registry.apps.${cluster_name}.${domain_name}/g" /etc/hosts
fi

# Create performance profile to allocate hugepages
cat <<EOF | oc apply -f -
apiVersion: performance.openshift.io/v1
kind: PerformanceProfile
metadata:
  name: performance
spec:
  cpu:
    isolated: "2-5"
    reserved: "0-1"
  hugepages:
    defaultHugepagesSize: 2M
    pages:
      - count: 16
        size: 2M
  nodeSelector:
    node-role.kubernetes.io/worker: ""
  realTimeKernel:
    enabled: false
EOF

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
export OPERATOR_WEBHOOK_NETWORK_POLICY_PORT=${OPERATOR_WEBHOOK_NETWORK_POLICY_PORT:-"6443"}
export INJECTOR_WEBHOOK_NETWORK_POLICY_PORT=${INJECTOR_WEBHOOK_NETWORK_POLICY_PORT:-"6443"}
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

# Keep raw operator JSON out of xtrace; log the standard operator table instead.
get_clusteroperator_readiness() (
  set +x
  kubectl get clusteroperator >&2 || return
  kubectl get clusteroperator -o json | jq -r '[
    ([.items[] | select(any(.status.conditions[]?; .type == "Available" and .status == "True") | not)] | length),
    ([.items[] | select(any(.status.conditions[]?; .type == "Progressing" and .status == "False") | not)] | length)
  ] | @tsv'
)

echo "## wait for the all cluster to be stable"
MAX_RETRIES=30
DELAY_SECONDS=10
retries=0
until [ $retries -ge $MAX_RETRIES ]; do
  # wait for all the openshift cluster operators to be available and not progressing
  if kubectl get nodes >/dev/null 2>&1; then
    if readiness=$(get_clusteroperator_readiness); then
      read -r not_available progressing <<< "$readiness"
      if [ "$not_available" -eq 0 ] && [ "$progressing" -eq 0 ]; then
        break
      fi
      echo "cluster operators are not ready (unavailable=$not_available, progressing=$progressing). Retrying... (Attempt $retries/$MAX_RETRIES)"
    else
      echo "Unable to query cluster operators. Retrying... (Attempt $retries/$MAX_RETRIES)"
    fi
  else
    echo "API not reachable. Retrying... (Attempt $retries/$MAX_RETRIES)"
  fi
  retries=$((retries+1))
  sleep $DELAY_SECONDS
done

if [ $retries -eq $MAX_RETRIES ]; then
  echo "Max retries reached. Exiting..."
  exit 1
fi

master_node_name="${cluster_name}-ctlplane-0.${domain_name}"
echo "## capturing pre-TechPreview master MachineConfig"
MAX_RETRIES=20
DELAY_SECONDS=10
retries=0
pre_techpreview_master_config=""
until [ $retries -ge $MAX_RETRIES ]; do
  pre_techpreview_master_config=$(kubectl get node "${master_node_name}" -o jsonpath='{.metadata.annotations.machineconfiguration\.openshift\.io/currentConfig}' 2>/dev/null || true)
  if [ -n "$pre_techpreview_master_config" ]; then
    break
  fi
  retries=$((retries+1))
  echo "Pre-TechPreview master MachineConfig is not available yet. Retrying... (Attempt $retries/$MAX_RETRIES)"
  sleep $DELAY_SECONDS
done

if [ -z "$pre_techpreview_master_config" ]; then
  echo "Failed to capture pre-TechPreview master MachineConfig. Exiting..."
  exit 1
fi

echo "## enabling TechPreviewNoUpgrade feature set"
kubectl patch featuregate cluster --type merge -p '{"spec":{"featureSet":"TechPreviewNoUpgrade"}}'

# Trigger CRIOCredentialProviderConfig early to avoid a late MCO-driven master reboot
# during the test phase. The CRD is created by the CVO after TechPreview is enabled,
# so we wait for it and then create the resource to force the MCO reboot now
# instead of mid-test.
echo "## waiting for CRIOCredentialProviderConfig CRD and creating resource"
for i in $(seq 1 30); do
  if kubectl get crd criocredentialproviderconfigs.config.openshift.io &>/dev/null; then
    echo "CRD available, creating CRIOCredentialProviderConfig resource"
    cat <<CRIOPROVIDER | kubectl apply -f - 2>/dev/null || true
apiVersion: config.openshift.io/v1alpha1
kind: CRIOCredentialProviderConfig
metadata:
  name: cluster
spec: {}
CRIOPROVIDER
    break
  fi
  echo "Waiting for CRIOCredentialProviderConfig CRD... (Attempt $i/30)"
  sleep 10
done

echo "## waiting for TechPreview-triggered master MachineConfig rollout"
MAX_RETRIES=80
DELAY_SECONDS=30
retries=0
techpreview_rollout_seen=false
until [ $retries -ge $MAX_RETRIES ]; do
  if kubectl get nodes >/dev/null 2>&1; then
    master_current_config=$(kubectl get node "${master_node_name}" -o jsonpath='{.metadata.annotations.machineconfiguration\.openshift\.io/currentConfig}' 2>/dev/null || true)
    master_desired_config=$(kubectl get node "${master_node_name}" -o jsonpath='{.metadata.annotations.machineconfiguration\.openshift\.io/desiredConfig}' 2>/dev/null || true)
    master_state=$(kubectl get node "${master_node_name}" -o jsonpath='{.metadata.annotations.machineconfiguration\.openshift\.io/state}' 2>/dev/null || true)
    if readiness=$(get_clusteroperator_readiness) && mcp_output=$(kubectl get mcp --no-headers 2>/dev/null); then
      read -r not_available progressing <<< "$readiness"
      updating=$(printf '%s\n' "$mcp_output" | awk '{print $4}' | grep -c True || true)
      if [ -n "$master_desired_config" ] && [ "$master_desired_config" != "$pre_techpreview_master_config" ]; then
        techpreview_rollout_seen=true
      fi
      if [ "$techpreview_rollout_seen" = true ] && [ "$master_current_config" = "$master_desired_config" ] && [ "$master_state" = "Done" ] && [ "$not_available" -eq 0 ] && [ "$progressing" -eq 0 ] && [ "$updating" -eq 0 ]; then
        echo "Cluster is stable after TechPreview enablement and master MachineConfig rollout completed"
        break
      fi
      echo "Cluster not yet stable after TechPreview (rollout_seen=$techpreview_rollout_seen, current=$master_current_config, desired=$master_desired_config, state=$master_state, unavailable=$not_available, progressing=$progressing, mcp_updating=$updating). Retrying... (Attempt $retries/$MAX_RETRIES)"
    else
      echo "Unable to query cluster operators or MachineConfigPools. Retrying... (Attempt $retries/$MAX_RETRIES)"
    fi
  else
    echo "API not reachable. Retrying... (Attempt $retries/$MAX_RETRIES)"
  fi
  retries=$((retries+1))
  sleep $DELAY_SECONDS
done

if [ $retries -eq $MAX_RETRIES ]; then
  echo "Max retries reached waiting for TechPreview MachineConfig rollout. Exiting..."
  exit 1
fi

echo "## wait for MachineConfigPools to finish updating"
retries=0
until [ $retries -ge $MAX_RETRIES ]; do
  updating=$(kubectl get mcp --no-headers 2>/dev/null | awk '{print $4}' | grep -c True || true)
  if [ "$updating" -eq 0 ]; then
    echo "All MachineConfigPools are stable"
    break
  fi
  retries=$((retries+1))
  echo "MachineConfigPools still updating. Retrying... (Attempt $retries/$MAX_RETRIES)"
  sleep $DELAY_SECONDS
done

echo "## wait for registry to be available"
kubectl wait configs.imageregistry.operator.openshift.io/cluster --for=condition=Available --timeout=120s

echo "## resolve the dedicated third NIC and native test PFs"
oc create namespace mock-smartnic-build --dry-run=client -o yaml | oc apply -f -
oc label namespace mock-smartnic-build pod-security.kubernetes.io/enforce=privileged --overwrite
# Namespace creation returns before the service account controller has reconciled it.
echo "## wait for the debug pod service account"
retries=0
until oc get serviceaccount default -n mock-smartnic-build >/dev/null 2>&1; do
  retries=$((retries+1))
  if [ "$retries" -ge 24 ]; then
    echo "Timed out waiting for mock-smartnic-build/default service account"
    exit 1
  fi
  sleep 5
done
: > "$MOCK_DRIVER_WORKDIR/targets.tsv"
: > "$MOCK_DRIVER_WORKDIR/native.tsv"
for ((num=0; num<NUM_OF_WORKERS; num++)); do
  node="$cluster_name-worker-$num.$domain_name"
  printf -v native_mac '52:54:00:fb:%02x:02' "$num"
  printf -v mock_mac '52:54:00:fb:%02x:03' "$num"
  printf -v extra_mac '52:54:00:fb:%02x:04' "$num"
  # oc debug can return success when the node command failed; require its marker.
  # shellcheck disable=SC2016
  timeout 180 oc debug -q -T -I "node/$node" --to-namespace=mock-smartnic-build -- chroot /host bash -ceu '
    for mac in "$@"; do
      names=()
      for path in /sys/class/net/*; do
        [[ $(cat "$path/address") != "$mac" ]] || names+=("${path##*/}")
      done
      [[ ${#names[@]} == 1 ]]
      p=$(readlink -f "/sys/class/net/${names[0]}/device")
      [[ $(basename "$(readlink -f "$p/driver")") == igb && $(cat "$p/sriov_numvfs") == 0 ]]
      if [[ $mac == "$2" ]]; then printf "TARGET %s %s\n" "${p##*/}" "$mac"
      else printf "NATIVE %s\n" "${names[0]}"; fi
    done
    echo MOCK_RESOLVE_OK
  ' -- "$native_mac" "$mock_mac" "$extra_mac" | tee "$MOCK_DRIVER_WORKDIR/$node-resolve.log"
  [[ $(tail -n 1 "$MOCK_DRIVER_WORKDIR/$node-resolve.log") == MOCK_RESOLVE_OK ]] || exit 1
  awk -v node="$node" '$1 == "TARGET" {print node, $2, $3}' "$MOCK_DRIVER_WORKDIR/$node-resolve.log" >> "$MOCK_DRIVER_WORKDIR/targets.tsv"
  awk -v node="$node" '$1 == "NATIVE" {print node ":" $2}' "$MOCK_DRIVER_WORKDIR/$node-resolve.log" >> "$MOCK_DRIVER_WORKDIR/native.tsv"
done
export SRIOV_NODE_AND_DEVICE_NAME_FILTER
SRIOV_NODE_AND_DEVICE_NAME_FILTER=$(python3 - "$MOCK_DRIVER_WORKDIR/native.tsv" <<'PY'
import re, sys
print('^(?:' + '|'.join(re.escape(line.strip()) for line in open(sys.argv[1])) + ')$')
PY
)
echo "## build, deploy before kubelet and verify the mock driver on all workers"
(
# Isolate build variables and disable tracing before reading registry credentials.
set +x
set -Eeuo pipefail
fail() { echo "ERROR: $*" >&2; exit 1; }
targets=$MOCK_DRIVER_WORKDIR/targets.tsv
source_dir=$(realpath "$MOCK_DRIVER_SOURCE")
work=$MOCK_DRIVER_WORKDIR
ns=mock-smartnic-build
pod=mock-driver-build
mc=99-worker-mock-smartnic
network=$cluster_name
# Validate the full worker scope before creating resources or running node commands.
oc get nodes -o json > "$work/nodes.json"
python3 - "$targets" "$work/nodes.json" <<'PY'
import json, re, sys
rows = [line.split() for line in open(sys.argv[1]) if line.strip() and not line.lstrip().startswith('#')]
nodes = json.load(open(sys.argv[2]))['items']
workers = {n['metadata']['name'] for n in nodes
           if 'node-role.kubernetes.io/worker' in n['metadata']['labels']
           and not {'node-role.kubernetes.io/master', 'node-role.kubernetes.io/control-plane'} & n['metadata']['labels'].keys()}
assert rows and all(len(r) == 3 for r in rows), 'Need hostname BDF MAC rows'
assert len({r[0] for r in rows}) == len(rows), 'Duplicate workers'
assert {r[0] for r in rows} == workers, 'Targets must cover exactly the worker-only nodes'
for node, bdf, mac in rows:
    assert re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9.-]*', node), 'Invalid hostname'
    assert re.fullmatch(r'[0-9a-f]{4}:[0-9a-f]{2}:[01][0-9a-f]\.[0-7]', bdf), 'Invalid PF BDF'
    assert re.fullmatch(r'(?:[0-9a-f]{2}:){5}[0-9a-f]{2}', mac), 'Invalid MAC'
kernels = {n['status']['nodeInfo']['kernelVersion'] for n in nodes if n['metadata']['name'] in workers}
assert len(kernels) == 1, 'All workers must run the same kernel for this image'
kernel = kernels.pop()
assert re.fullmatch(r'[a-zA-Z0-9_.+-]+', kernel), 'Invalid kernel release'
print('Workers:', ', '.join(sorted(workers)), 'kernel:', kernel)
PY
read -r build_node kernel < <(python3 - "$work/nodes.json" "$targets" <<'PY'
import json, sys
name = next(l.split()[0] for l in open(sys.argv[2]) if l.strip() and not l.lstrip().startswith('#'))
print(name, next(n['status']['nodeInfo']['kernelVersion'] for n in json.load(open(sys.argv[1]))['items'] if n['metadata']['name'] == name))
PY
)
while read -r node bdf mac || [[ -n $node ]]; do
  [[ -n $node && $node != \#* ]] || continue
  # kcli uses the system connection; non-root virsh can default to the session.
  virsh --connect qemu:///system dumpxml "${node%%.*}" > "$work/$node.xml"
  python3 - "$work/$node.xml" "$network" "$bdf" "$mac" <<'PY'
import sys, xml.etree.ElementTree as E
path, network, bdf, mac = sys.argv[1:]
x = E.parse(path).getroot()
assert not x.findall('./devices/hostdev') and not x.findall('./devices/interface[@type="hostdev"]'), 'PCI passthrough is unsupported'
nics = [n for n in x.findall('./devices/interface') if n.find('mac').get('address') == mac]
assert len(nics) == 1, 'Exact MAC must identify one emulated NIC'
n = nics[0]
assert n.find('model').get('type') == 'igb' and n.find('source').get('network') == network, 'Expected dedicated emulated igb network'
# Libvirt PCI bus values are controller indices, not guest BDF bus numbers.
# The host preflight maps this attested MAC to the exact requested guest BDF.
print('Attested:', x.findtext('name'), mac)
PY
done < "$work/targets.tsv"

host() {
  local node=$1 log="$work/host-$1.log"
  local input=(-I)
  shift
  if [[ ${1:-} == --stdin ]]; then input=(); shift; fi
  # oc debug does not propagate the command's exit status. Require its last line.
  # shellcheck disable=SC2016
  timeout 180 oc debug -q -T "${input[@]}" "node/$node" --to-namespace="$ns" \
    -- chroot /host bash -ceu '"$@"; echo MOCK_SMARTNIC_HOST_OK' -- "$@" | tee "$log"
  [[ $(tail -n 1 "$log") == MOCK_SMARTNIC_HOST_OK ]] || fail "Node command failed on $node; see $log"
}
  toolkit=$(oc get is -n openshift driver-toolkit -o jsonpath='{.status.tags[?(@.tag=="latest")].items[0].dockerImageReference}')
  [[ $toolkit == *@sha256:* ]] || fail 'Driver Toolkit digest is unavailable.'
  registry=$(oc get route -n openshift-image-registry default-route -o jsonpath='{.spec.host}')
  [[ $registry =~ ^[a-zA-Z0-9.-]+$ ]] || fail 'Internal registry route is unavailable.'
  oc create namespace "$ns" --dry-run=client -o yaml | oc apply -f -
  oc label namespace "$ns" pod-security.kubernetes.io/enforce=privileged --overwrite
  oc create serviceaccount mock-builder -n "$ns" --dry-run=client -o yaml | oc apply -f -
  oc create serviceaccount mock-loader -n "$ns" --dry-run=client -o yaml | oc apply -f -
  oc create rolebinding mock-builder-privileged -n "$ns" --clusterrole=system:openshift:scc:privileged --serviceaccount="$ns:mock-builder" --dry-run=client -o yaml | oc apply -f -
  oc create rolebinding mock-builder-push -n "$ns" --clusterrole=system:image-builder --serviceaccount="$ns:mock-builder" --dry-run=client -o yaml | oc apply -f -
  oc create rolebinding mock-loader-pull -n "$ns" --clusterrole=system:image-puller --serviceaccount="$ns:mock-loader" --dry-run=client -o yaml | oc apply -f -
  # Keep registry credentials in Secrets and root-only artifacts; never trace them.
  oc get secret pull-secret -n openshift-config -o json | python3 -c '
import json,sys
s=json.load(sys.stdin); s["metadata"]={"name":"registry-pull","namespace":"mock-smartnic-build"}; s.pop("immutable",None)
json.dump(s,sys.stdout)' | oc apply -f -
  oc get secret router-ca -n openshift-ingress-operator -o jsonpath='{.data.tls\.crt}' | base64 -d > "$work/ca.crt"
  oc create secret generic registry-ca -n "$ns" --from-file="$work/ca.crt" --dry-run=client -o yaml | oc apply -f -
  # The dedicated hostPath keeps Podman's cache across worker reboots/drains.
  phase=$(oc get pod "$pod" -n "$ns" --ignore-not-found -o jsonpath='{.status.phase}')
  placement=$(oc get pod "$pod" -n "$ns" --ignore-not-found -o jsonpath='{.spec.nodeSelector}')
  if [[ $phase == Failed || $phase == Succeeded || $placement == *node-role.kubernetes.io/worker* ]]; then
    oc delete pod "$pod" -n "$ns" --wait=true --timeout=120s
  fi
  cat <<EOF | oc apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: $pod
  namespace: $ns
spec:
  serviceAccountName: mock-builder
  restartPolicy: Never
  nodeSelector:
    kubernetes.io/hostname: $build_node
  containers:
  - name: podman
    image: quay.io/podman/stable
    command: [sleep, infinity]
    securityContext:
      privileged: true
    volumeMounts:
    - {name: pull, mountPath: /auth, readOnly: true}
    - {name: ca, mountPath: /certs, readOnly: true}
    - {name: storage, mountPath: /var/lib/containers}
  volumes:
  - name: pull
    secret: {secretName: registry-pull}
  - name: ca
    secret: {secretName: registry-ca}
  - name: storage
    hostPath:
      path: /var/lib/mock-smartnic-build
      type: DirectoryOrCreate
EOF
  oc wait -n "$ns" "pod/$pod" --for=condition=Ready --timeout=300s
  while read -r node bdf mac || [[ -n $node ]]; do
    [[ -n $node && $node != \#* ]] || continue
    # Run the existing guard on the host where OVS ownership is visible.
    { cat "$source_dir/scripts/lib/pf-guard.sh"; cat <<'PREFLIGHT'
set -Eeuo pipefail
fail() { echo "ERROR: $*" >&2; exit 1; }
p=/sys/bus/pci/devices/$1
[[ $(uname -r) == "$3" && $(cat "$p/vendor") == 0x8086 && $(cat "$p/device") == 0x10c9 && ! -e $p/physfn ]]
[[ $(cat /proc/sys/kernel/modules_disabled) == 0 ]]
[[ ! -r /sys/kernel/security/lockdown || $(cat /sys/kernel/security/lockdown) == *'[none]'* ]]
if [[ $(basename "$(readlink -f "$p/driver")") == mock_smartnic_pf ]]; then
  [[ $(cat /sys/module/mock_smartnic/parameters/target_pf) == "$1" ]]
else
  [[ $(basename "$(readlink -f "$p/driver")") == igb && $(cat "$p/sriov_numvfs") == 0 ]]
  mapfile -t names < <(find "$p/net" -mindepth 1 -maxdepth 1 -printf '%f\n')
  [[ ${#names[@]} == 1 && $(cat "/sys/class/net/${names[0]}/address") == "$2" ]]
  not_used "${names[0]}"
fi
echo "Preflight passed: $(hostname) $1"
PREFLIGHT
    } | host "$node" --stdin bash -s -- "$bdf" "$mac" "$kernel"
  done < "$work/targets.tsv"
  rm -rf "$work/context"
  mkdir -p "$work/context/driver"
  cp "$source_dir"/driver/*.[ch] "$source_dir/driver/Makefile" "$work/context/driver/"
  cp "$source_dir/scripts/lib/pf-guard.sh" "$work/context/"
  cat > "$work/context/load-driver" <<'LOAD'
#!/usr/bin/env bash
set -Eeuo pipefail
fail() { echo "ERROR: $*" >&2; exit 1; }
source /opt/mock/pf-guard.sh
bdf=$1 mac=$2
p=/sys/bus/pci/devices/$bdf
[[ $(modinfo -F vermagic /opt/mock/mock_smartnic.ko) == "$(uname -r) "* ]] || fail 'Rebuild the image for this kernel.'
[[ $(cat /proc/sys/kernel/modules_disabled) == 0 ]]
[[ ! -r /sys/kernel/security/lockdown || $(cat /sys/kernel/security/lockdown) == *'[none]'* ]] || fail 'An authorized signed module is required.'
[[ $(cat "$p/vendor") == 0x8086 && $(cat "$p/device") == 0x10c9 && ! -e $p/physfn ]]
if [[ $(basename "$(readlink -f "$p/driver")") == mock_smartnic_pf ]]; then
  [[ $(cat /sys/module/mock_smartnic/parameters/target_pf) == "$bdf" ]]
  ip link set "$(find "$p/net" -mindepth 1 -maxdepth 1 -printf '%f\n')" up
  exit 0
fi
[[ $(basename "$(readlink -f "$p/driver")") == igb && $(cat "$p/sriov_numvfs") == 0 ]]
mapfile -t names < <(find "$p/net" -mindepth 1 -maxdepth 1 -printf '%f\n')
[[ ${#names[@]} == 1 && $(cat "/sys/class/net/${names[0]}/address") == "$mac" ]]
not_used "${names[0]}" boot
[[ ! -d /sys/module/mock_smartnic ]] || fail 'Another mock PF is already loaded.'
[[ -z $(find /sys/bus/pci/drivers/igbvf -maxdepth 1 -type l -name '????:??:??.?' -print 2>/dev/null) ]] || fail 'Native VFs are already in use.'
mkdir -p "/state/$bdf"
printf '%s\n' "$mac" > "/state/$bdf/native-mac"
modprobe -r igbvf
modprobe sch_ingress; modprobe cls_flower; modprobe act_mirred
insmod /opt/mock/mock_smartnic.ko target_pf="$bdf" allow_igb_emulation=1
printf 'mock_smartnic_pf\n' > "$p/driver_override"
printf '%s\n' "$bdf" > "$p/driver/unbind"
if ! printf '%s\n' "$bdf" > /sys/bus/pci/drivers/mock_smartnic_pf/bind; then
  printf 'igb\n' > "$p/driver_override"
  printf '%s\n' "$bdf" > /sys/bus/pci/drivers/igb/bind || true
  fail 'Mock bind failed; native rebind attempted.'
fi
modprobe igbvf
printf '1\n' > "$p/sriov_drivers_autoprobe"
[[ $(basename "$(readlink -f "$p/driver")") == mock_smartnic_pf && $(cat "$p/sriov_numvfs") == 0 ]]
ip link set "$(find "$p/net" -mindepth 1 -maxdepth 1 -printf '%f\n')" up
echo "Loaded mock_smartnic: $bdf, $(uname -r), zero VFs"
LOAD
  cat > "$work/context/Containerfile" <<'CONTAINER'
ARG DTK
FROM ${DTK} AS build
ARG KERNEL
COPY driver /build/driver
RUN test -d /usr/src/kernels/${KERNEL} && make -C /usr/src/kernels/${KERNEL} M=/build/driver -j2 modules && test "$(modinfo -F vermagic /build/driver/mock_smartnic.ko | cut -d' ' -f1)" = "${KERNEL}"
FROM registry.access.redhat.com/ubi9/ubi
RUN dnf install -y kmod iproute && dnf clean all
COPY --from=build /build/driver/mock_smartnic.ko /opt/mock/mock_smartnic.ko
COPY pf-guard.sh load-driver /opt/mock/
ENTRYPOINT ["bash", "/opt/mock/load-driver"]
CONTAINER
  revision=$(git -C "$source_dir" rev-parse --short=12 HEAD)
  tag="$registry/$ns/mock-smartnic:$kernel-$revision-$(date -u +%Y%m%d%H%M%S)"
  tar -C "$work/context" -cf - . | oc exec -i -n "$ns" "$pod" -- bash -c 'mkdir -p /build; tar -C /build -xf -'
  # Expansions below belong to the shell in the build pod.
  # shellcheck disable=SC2016
  timeout 1800 oc exec -n "$ns" "$pod" -- bash -ceu '
    printf "%s" "$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" |
      podman --storage-driver=overlay login --cert-dir=/certs --authfile=/tmp/push-auth.json --username=serviceaccount --password-stdin "$3"
    podman --storage-driver=overlay build --authfile=/auth/.dockerconfigjson --build-arg="DTK=$1" --build-arg="KERNEL=$2" -t "$4" /build
    podman --storage-driver=overlay push --cert-dir=/certs --authfile=/tmp/push-auth.json --digestfile=/tmp/driver-digest "$4"
  ' -- "$toolkit" "$kernel" "$registry" "$tag" 2>&1 | tee "$work/build.log"
  digest=$(oc exec -n "$ns" "$pod" -- cat /tmp/driver-digest)
  [[ $digest =~ ^sha256:[0-9a-f]{64}$ ]] || fail 'Missing pushed image digest.'
  printf '%s\n' "$registry/$ns/mock-smartnic@$digest" > "$work/image"
  printf '%s\n' "$toolkit" > "$work/toolkit"
  echo "Built: $(cat "$work/image")"

  [[ -s $work/image && -s $work/ca.crt ]] || fail 'Run build first with the same work directory.'
  image=$(cat "$work/image")
  [[ $image =~ ^[a-zA-Z0-9.-]+/mock-smartnic-build/mock-smartnic@sha256:[0-9a-f]{64}$ ]] || fail 'Invalid built image reference.'
  while read -r node bdf mac || [[ -n $node ]]; do
    [[ -n $node && $node != \#* ]] || continue
    # Do not reboot workers with live VF allocations during driver iteration.
    # shellcheck disable=SC2016
    host "$node" bash -ceu '[[ $(cat "/sys/bus/pci/devices/$1/sriov_numvfs") == 0 ]]' -- "$bdf"
  done < "$work/targets.tsv"
  # Auto-generated dockercfg tokens rotate/expire on modern OCP. Boot needs a
  # durable token for the namespace-scoped image-puller account.
  cat <<EOF | oc apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: mock-loader-token
  namespace: $ns
  annotations:
    kubernetes.io/service-account.name: mock-loader
type: kubernetes.io/service-account-token
EOF
  encoded_token=
  for attempt in {1..30}; do
    encoded_token=$(oc get secret mock-loader-token -n "$ns" -o jsonpath='{.data.token}')
    [[ -z $encoded_token ]] || break
    sleep 2
  done
  [[ -n $encoded_token ]] || fail 'Loader registry credential is unavailable.'
  oc get secret mock-loader-token -n "$ns" -o json | python3 -c '
import base64,json,sys
token=base64.b64decode(json.load(sys.stdin)["data"]["token"]).decode()
claims=json.loads(base64.urlsafe_b64decode(token.split(".")[1]+"==="))
assert "exp" not in claims, "Boot credential must not expire"
auth=base64.b64encode(("serviceaccount:"+token).encode()).decode()
json.dump({"auths":{sys.argv[1]:{"auth":auth}}},sys.stdout)
' "${image%%/*}" > "$work/pull-auth.json"
  chmod 600 "$work/pull-auth.json"
  # The only ingress router may be on the worker being rebooted. Cache the exact
  # digest while kubelet/ingress are available, then use Podman's missing policy.
  while read -r node bdf mac || [[ -n $node ]]; do
    [[ -n $node && $node != \#* ]] || continue
    # shellcheck disable=SC2016
    tar -C "$work" -cf - ca.crt pull-auth.json | host "$node" --stdin bash -ceu '
      tmp=$(mktemp -d /var/tmp/mock-smartnic-prefetch.XXXXXX)
      trap '\''rm -rf "$tmp"'\'' EXIT
      tar -xf - -C "$tmp"
      podman pull --authfile="$tmp/pull-auth.json" --cert-dir="$tmp" "$1"
    ' -- "$image"
  done < "$work/targets.tsv"
  cat > "$work/start-driver" <<START
#!/usr/bin/env bash
set -Eeuo pipefail
read -r node bdf mac < <(awk -v node="\$(hostname)" '\$1 == node {print; found++} END {if (found != 1) exit 1}' /etc/mock-smartnic/targets.tsv)
[[ -n \$bdf && -n \$mac ]]
mkdir -p /var/lib/mock-smartnic-ocp
podman pull --policy=missing --authfile=/etc/mock-smartnic/pull-auth.json --cert-dir=/etc/mock-smartnic/certs '$image'
exec podman run --rm --pull=never --privileged --network=host --pid=host \\
  --volume=/sys:/sys --volume=/usr/lib/modules:/usr/lib/modules:ro \\
  --volume=/var/lib/mock-smartnic-ocp:/state '$image' "\$bdf" "\$mac"
START
  python3 - "$work" "$mc" <<'PY' > "$work/machineconfig.json"
import base64, json, pathlib, sys
w = pathlib.Path(sys.argv[1])
files = []
for src, dest, mode in [('start-driver','/usr/local/bin/mock-smartnic-start',0o755),('targets.tsv','/etc/mock-smartnic/targets.tsv',0o644),('ca.crt','/etc/mock-smartnic/certs/ca.crt',0o644),('pull-auth.json','/etc/mock-smartnic/pull-auth.json',0o600)]:
    files.append({'path':dest,'mode':mode,'contents':{'source':'data:;base64,'+base64.b64encode((w/src).read_bytes()).decode()}})
nm = '[device-mock-smartnic]\nmatch-device=driver:mock_smartnic_pf,driver:mock_smartnic_vf\nmanaged=0\n'
files.append({'path':'/etc/NetworkManager/conf.d/90-mock-smartnic-ocp.conf','mode':0o644,'contents':{'source':'data:;base64,'+base64.b64encode(nm.encode()).decode()}})
unit = '''[Unit]
Description=Load the kernel-matched mock SmartNIC on worker test PFs
Wants=network-online.target
After=network-online.target ovs-configuration.service
Before=kubelet.service sriov-config.service sriov-config-post-network.service
[Service]
Type=oneshot
RemainAfterExit=yes
TimeoutStartSec=300
ExecStart=/usr/local/bin/mock-smartnic-start
[Install]
WantedBy=multi-user.target
'''
dropin = '[Unit]\nRequires=mock-smartnic.service\nAfter=mock-smartnic.service\n'
json.dump({'apiVersion':'machineconfiguration.openshift.io/v1','kind':'MachineConfig','metadata':{'name':sys.argv[2],'labels':{'machineconfiguration.openshift.io/role':'worker'}},'spec':{'config':{'ignition':{'version':'3.2.0'},'storage':{'files':files},'systemd':{'units':[{'name':'mock-smartnic.service','enabled':True,'contents':unit},{'name':'kubelet.service','dropins':[{'name':'30-mock-smartnic.conf','contents':dropin}]}]}}}},sys.stdout,indent=2)
PY
  chmod 600 "$work/machineconfig.json"
  previous_pool=$(oc get mcp worker -o jsonpath='{.spec.configuration.name}')
  oc get mc "$mc" --ignore-not-found -o json > "$work/previous-machineconfig.json"
  changed=$(python3 - "$work/previous-machineconfig.json" "$work/machineconfig.json" <<'PY'
import json, pathlib, sys
old=pathlib.Path(sys.argv[1]).read_text().strip()
print('yes' if not old or json.loads(old)['spec'] != json.load(open(sys.argv[2]))['spec'] else 'no')
PY
)
  chmod 600 "$work/previous-machineconfig.json"
  oc apply -f "$work/machineconfig.json"
  # Wait for inclusion in the rendered pool, not an old Updated=True condition.
  for attempt in {1..240}; do
    oc get mcp worker -o json > "$work/pool.json"
    if python3 - "$work/pool.json" "$mc" "$previous_pool" "$changed" <<'PY'
import json, sys
p=json.load(open(sys.argv[1]))
included=any(s['name']==sys.argv[2] for s in p['status']['configuration']['source'])
rendered=p['status']['configuration']['name'] == p['spec']['configuration']['name']
changed=sys.argv[4]=='no' or p['spec']['configuration']['name'] != sys.argv[3]
conditions={c['type']:c['status'] for c in p['status']['conditions']}
sys.exit(0 if included and rendered and changed and conditions.get('Updated')=='True' and conditions.get('Updating')=='False' and conditions.get('Degraded')=='False' and p['status']['updatedMachineCount']==p['status']['machineCount'] else 1)
PY
    then break; fi
    ((attempt < 240)) || fail 'Worker MachineConfig rollout timed out; inspect mcp and node journals.'
    echo "Waiting for worker MachineConfig rollout ($attempt/240)"
    sleep 10
  done
  rm -f "$work/pull-auth.json"

  # An owned bridge and two real VF endpoints; preserve the existing OVN bridges.
  while read -r node bdf mac || [[ -n $node ]]; do
    [[ -n $node && $node != \#* ]] || continue
    # Expansions below belong to the shell on the worker.
    # shellcheck disable=SC2016
    host "$node" bash -ceu '
      p=/sys/bus/pci/devices/$1
      systemctl is-active mock-smartnic.service
      loaded=$(systemctl show mock-smartnic.service -p ExecMainExitTimestampMonotonic --value)
      kubelet=$(systemctl show kubelet.service -p ExecMainStartTimestampMonotonic --value)
      ((loaded > 0 && kubelet > loaded))
      [[ $(cat /sys/module/mock_smartnic/parameters/target_pf) == "$1" ]]
      [[ $(basename "$(readlink -f "$p/driver")") == mock_smartnic_pf && $(cat "$p/sriov_numvfs") == 0 ]]
      [[ ! -e /run/netns/mock-ocp-vf0 && ! -e /run/netns/mock-ocp-vf1 ]]
      ! ovs-vsctl br-exists mock-ocp-test
      cleanup() {
        ovs-vsctl --if-exists del-br mock-ocp-test
        for n in mock-ocp-vf0 mock-ocp-vf1; do
          if [[ -e /run/netns/$n ]]; then ip netns del "$n"; fi
        done
        echo 0 > "$p/sriov_numvfs"
        devlink dev eswitch set "pci/$1" mode legacy
        echo 1 > "$p/sriov_drivers_autoprobe"
      }
      trap "cleanup $1" EXIT
      echo 2 > "$p/sriov_numvfs"
      devlink dev eswitch set "pci/$1" mode switchdev
      devlink dev eswitch show "pci/$1"
      ports=$(devlink -j port show)
      r0=$(jq -er --arg p "pci/$1/1" ".port[\$p].netdev" <<< "$ports")
      r1=$(jq -er --arg p "pci/$1/2" ".port[\$p].netdev" <<< "$ports")
      ovs-vsctl add-br mock-ocp-test -- add-port mock-ocp-test "$r0" -- add-port mock-ocp-test "$r1"
      ip link set "$r0" up; ip link set "$r1" up
      for i in 0 1; do
        vf=$(readlink -f "$p/virtfn$i")
        [[ $(basename "$(readlink -f "$vf/driver")") == mock_smartnic_vf ]]
        v=$(find "$vf/net" -mindepth 1 -maxdepth 1 -printf "%f\n")
        ip netns add "mock-ocp-vf$i"
        ip link set "$v" netns "mock-ocp-vf$i"
        ip -n "mock-ocp-vf$i" link set lo up
        ip -n "mock-ocp-vf$i" link set "$v" up
        ip -n "mock-ocp-vf$i" addr add "198.18.0.$((i+1))/24" dev "$v"
      done
      ip netns exec mock-ocp-vf0 ping -c 3 -W 2 198.18.0.2
      ip netns exec mock-ocp-vf1 ping -c 3 -W 2 198.18.0.1
      ovs-vsctl list-ports mock-ocp-test
      cleanup "$1"; trap - EXIT
      [[ $(cat "$p/sriov_numvfs") == 0 && $(cat "$p/sriov_drivers_autoprobe") == 1 ]]
      echo "PASS: $(hostname) switchdev, two real PCI VFs, representors, OVS, bidirectional ping; restored zero VFs/legacy"
    ' -- "$bdf" | tee "$work/$node-verify.log"
  done < "$work/targets.tsv"
  oc get nodes
  oc get mcp
echo "Artifacts: $work (machineconfig.json contains a registry pull credential; keep private)"
)

oc create token builder -n "$NAMESPACE" --duration=2h |
  podman login -u serviceaccount --password-stdin "$registry" --tls-verify=false

MAX_RETRIES=20
DELAY_SECONDS=10
retry_push() {
  local retries=0

  until [ $retries -ge $MAX_RETRIES ]; do
    podman push --tls-verify=false "$@" && break
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

if [[ -v LOCAL_OVS_CNI_IMAGE ]]; then
  podman_tag_and_push "$LOCAL_OVS_CNI_IMAGE" "$registry/$NAMESPACE/ovs-cni:latest"
  export OVS_CNI_IMAGE="image-registry.openshift-image-registry.svc:5000/$NAMESPACE/ovs-cni:latest"
fi

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
