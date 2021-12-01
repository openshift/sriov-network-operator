#!/usr/bin/env bash
set -eo pipefail
here="$(dirname "$(readlink --canonicalize "${BASH_SOURCE[0]}")")"
root="$(readlink --canonicalize "$here/..")"
export SRIOV_NETWORK_OPERATOR_IMAGE="${SRIOV_NETWORK_OPERATOR_IMAGE:-sriov-network-operator:latest}"
export SRIOV_NETWORK_CONFIG_DAEMON_IMAGE="${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE:-origin-sriov-network-config-daemon:latest}"
export KUBECONFIG="${KUBECONFIG:-${HOME}/.kube/config}"
INTERFACES_SWITCHER="${INTERFACES_SWITCHER:-"test-suite"}"
SUPPORTED_INTERFACE_SWTICHER_MODES=("test-suite", "system-service")
RETRY_MAX=10
INTERVAL=10
TIMEOUT=300
MULTUS_CNI_DS="https://raw.githubusercontent.com/intel/multus-cni/master/deployments/multus-daemonset.yml"

while test $# -gt 0; do
    case "$1" in
    --device-netns-switcher)
        INTERFACES_SWITCHER="$2"
        if [[ ! "${SUPPORTED_INTERFACE_SWTICHER_MODES[@]}" =~ "${INTERFACES_SWITCHER}" ]]; then
            echo "Error: unsupported interface switching mode: ${INTERFACES_SWITCHER}!"
            echo "Supported modes are: ${SUPPORTED_INTERFACE_SWTICHER_MODES[@]}"
            exit 1
        fi
        shift
        shift
        ;;

    --pf-pci-address)
        test_pf_pci_addr="$2"
        shift
        shift
        ;;

    *)
        if [[ -z "$test_pf_pci_addr" ]];then
            test_pf_pci_addr=$1
        fi
        shift
    ;;
    esac
done

check_requirements() {
  for cmd in docker kind kubectl ip; do
    if ! command -v "$cmd" &> /dev/null; then
      echo "$cmd is not available"
      exit 1
    fi
  done

  if [ "$test_pf_pci_addr" == "" ]; then
    echo "specify a physical function PCI address as an argument"
    echo "e.g. $0 0000:01:00.0"
    exit 1
  fi
  return 0
}

retry() {
  local status=0
  local retries=${RETRY_MAX:=5}
  local delay=${INTERVAL:=5}
  local to=${TIMEOUT:=20}
  cmd="$*"

  while [ $retries -gt 0 ]
  do
    status=0
    timeout $to bash -c "echo $cmd && $cmd" || status=$?
    if [ $status -eq 0 ]; then
      break;
    fi
    echo "Exit code: '$status'. Sleeping '$delay' seconds before retrying"
    sleep $delay
    (( retries-- ))
  done
  return $status
}

echo "## checking requirements"
check_requirements
echo "## delete any existing cluster, deploy control & data plane cluster with KinD"
retry kind delete cluster &&  cat <<EOF | kind create cluster --kubeconfig=${KUBECONFIG} --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
EOF
sudo chmod 644 ${KUBECONFIG}
echo "## build operator image"
retry docker build -t "${SRIOV_NETWORK_OPERATOR_IMAGE}" -f "${root}/Dockerfile" "${root}"
echo "## load operator image into KinD"
kind load docker-image "${SRIOV_NETWORK_OPERATOR_IMAGE}"
echo "## build daemon image"
retry docker build -t "${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}" -f "${root}/Dockerfile.sriov-network-config-daemon" "${root}"
echo "## load daemon image into KinD"
kind load docker-image "${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}"
echo "## export kube config for utilising locally"
kind export kubeconfig
echo "## wait for coredns"
retry kubectl -n kube-system wait --for=condition=available deploy/coredns --timeout=${TIMEOUT}s
echo "## install multus"
retry kubectl create -f "$MULTUS_CNI_DS"
echo "## wait for multus"
retry kubectl -n kube-system wait --for=condition=ready -l name=multus pod --timeout=${TIMEOUT}s
echo "## find KinD container"
kind_container="$(docker ps -q --filter 'name=kind-worker')"
echo "## validate KinD cluster formed"
[ "$kind_container" == "" ] && echo "could not find a KinD container 'kind-worker'" && exit 5
echo "## make KinD's sysfs writable (required to create VFs)"
docker exec "$kind_container" mount -o remount,rw /sys
echo "## label KinD's control-plane-node as sriov capable"
kubectl label node kind-worker feature.node.kubernetes.io/network-sriov.capable=true --overwrite
echo "## label KinD worker as worker"
kubectl label node kind-worker node-role.kubernetes.io/worker= --overwrite
if [[ "${INTERFACES_SWITCHER}" == "system-service" ]];then
    pf="$(ls /sys/bus/pci/devices/${test_pf_pci_addr}/net)"
    mkdir -p /etc/vf-switcher
    cat <<EOF > /etc/vf-switcher/vf-switcher.yaml
[
  {
     "netns": "${kind_container}",
     "pfs": [
        "${pf}"
      ]
   }
]
EOF
    sudo systemctl restart vf-switcher.service
else
    echo "## retrieving netns path from container"
    netns_path="$(sudo docker inspect --format '{{ .NetworkSettings.SandboxKey }}' "${kind_container}")"
    echo "## exporting test device '${test_pf_pci_addr}' and test netns path '${netns_path}'"
    export TEST_PCI_DEVICE="${test_pf_pci_addr}"
    export TEST_NETNS_PATH="${netns_path}"
fi
echo "## disabling webhooks"
export ENABLE_ADMISSION_CONTROLLER=false
echo "## deploying SRIOV Network Operator"
make --directory "${root}" deploy-setup-k8s
echo "## wait for sriov-network-config-daemon to be ready"
retry kubectl -n sriov-network-operator wait --for=condition=ready -l app=sriov-network-config-daemon pod --timeout=${TIMEOUT}s
echo "## Executing E2E tests"
make --directory "${root}" test-e2e-k8s
