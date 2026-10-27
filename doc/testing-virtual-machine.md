## E2E conformance test

It's possible to use QEMU to test the SR-IOV operator on a virtual kubernetes/openshift cluster.
Using the IGB model network driver allow to create virtual functions on the virtual system

## How to test

First you will need to enable the `DEV_MODE` via the operator environment variable.
Second step is to add the intel virtual nic to the supported nics configmap.

VFIO tests require an isolated IOMMU group on the guest. The switchdev mock lane
uses kernel netdevices and does not use VFIO or unsafe no-IOMMU mode.

### Prerequisites
* kcli - deployment tool (https://github.com/karmab/kcli)
* virsh 
* qemu > 8.1
* libvirt > 9
* podman
* make
* go

*Note:* For openshift you will also need a pull secret that you can download from the [Redhat Console](https://console.redhat.com/)

## Deploy the cluster

use the deployment [script](../hack/run-e2e-conformance-virtual-cluster.sh), this will deploy a k8s cluster
compile the operator images and run the e2e tests.

example:
```
SKIP_DELETE=TRUE make test-e2e-conformance-virtual-k8s-cluster 
```

It's also possible to skip the tests and only deploy the cluster running

```
SKIP_TEST=TRUE SKIP_DELETE=TRUE make test-e2e-conformance-virtual-k8s-cluster
```

To use the cluster after the deployment you need to export the kubeconfig

```
export KUBECONFIG=$HOME/.kcli/clusters/virtual/auth/kubeconfig
```

It's possible to also configure the number of workers using `NUM_OF_WORKERS`.

*NOTE:* For OCP then min number is 3 and for k8s is 2.

To work on the operator you can change the code and rebuild the operator using
`make redeploy-operator-virtual-cluster`.
You need to tell the cluster type for ocp `CLUSTER_TYPE=openshift` and for k8s `CLUSTER_TYPE=kubernetes`

## Switchdev mock driver on Kubernetes

The virtual runner clones the `main` branch of [switchdev-mock-driver](https://github.com/SchSeba/switchdev-mock-driver)
on each worker, records its commit and runs its installer. Setup builds for the
running guest kernel using matching headers; no fixed release is required. The runner attests the dedicated QEMU
NICs on the same test network in libvirt XML using explicit MACs. Each worker
has three NICs: management `igb`, secondary `igb` (`sriovtest0`) for regular tests, and
third `mock_smartnic_pf` for switchdev. The management PF stays on its native
driver. Setup installs missing
OVS/build dependencies, builds the module, and persists binding before kubelet.
The operator and CNI create the final VFs, bridges, NADs and representor ports.

Run on the libvirt host; this deletes and recreates the named cluster:

```bash
MOCK_SMARTNIC_REPO=https://github.com/SchSeba/switchdev-mock-driver.git \
OVS_CNI_IMAGE=quay.io/schseba/ovs-cni-plugin:latest \
CLUSTER_TYPE=kubernetes SKIP_DELETE=TRUE \
make test-e2e-conformance-virtual-k8s-cluster
```

The installer preloads igbvf while the mock PF has zero VFs so native and mock
VF drivers coexist during operator unbind/rebind. The runner keeps `DEV_MODE=TRUE`
enabled for the emulated devices.
The runner enables the guest IOMMU for native VFIO tests; it never enables unsafe
no-IOMMU mode.

`OVS_CNI_IMAGE` is passed through Helm. The image must contain the generic
VF-info and representor discovery fix from the ovs-cni `fix_hwoffload` branch.
For a locally built image, set `LOCAL_OVS_CNI_IMAGE` instead; the runner pushes
it to the local registry.
Set `SKIP_TEST=TRUE` to provision first and run tests from a separate controller.
Its Go architecture must match that controller; for an ARM64 controller:

```bash
export OPERATOR_NAMESPACE=sriov-network-operator
export KUBECONFIG=/home/vscode/kubeconfig/virt-cluster-k8s
export GOARCH=arm64 GOOS=linux CLUSTER_TYPE=kubernetes
export CLUSTER_HAS_EMULATED_PF=TRUE
export SRIOV_NODE_AND_DEVICE_NAME_FILTER='^.*:sriovtest0$'
SUITE=./test/conformance hack/run-e2e-conformance.sh
```

The runner sets the existing device filter to native `sriovtest0`. Set the same
filter when running tests from a separate controller, as above.
Switchdev selection bypasses that device-name filter while respecting
`NODES_SELECTOR`. It reuses the unused-device check to exclude default-route
interfaces and OVS ports, then selects supported switchdev drivers
(`mlx5_core`, `ice`, `mock_smartnic_pf`). The same test configures the pool,
five-VF policy and managed bridge, creates two OVS-connected pods, and verifies
bidirectional ping, increasing offloaded IPv4 counters and TC `in_hw` packet
statistics. It does not use mock debugfs or a driver-specific test branch.
Trust, VLAN/QoS, IPv6 and jumbo-frame tests run on native igb.

## Switchdev mock driver on OpenShift

`make test-e2e-conformance-virtual-ocp-cluster` recreates the OCP cluster, builds
the operator, deploys the mock driver and runs conformance. Each worker has
management virtio, a native `igb` PF, the third NIC bound to `mock_smartnic_pf`,
and an additional native `igb` PF. Explicit MACs identify the mock PF; regular
tests use both native PFs and switchdev tests discover the mock separately.

The runner uses the sibling `switchdev-mock-driver` checkout when present,
otherwise clones its `main` branch. Set `MOCK_DRIVER_SOURCE` to use another
checkout or `MOCK_SMARTNIC_REPO` to clone another HTTPS repository.
The [OCP runner](../hack/run-e2e-conformance-virtual-ocp.sh) builds for the
worker kernel in a privileged Podman pod using the cluster's Driver Toolkit.
It pushes to the internal registry, caches the image on every worker, and
applies a worker-only MachineConfig to load it before kubelet. RHCOS receives
no development packages. Setup verifies switchdev, VFs and OVS connectivity,
then restores zero VFs and legacy mode before operator deployment.

`OVS_CNI_IMAGE` defaults to `quay.io/schseba/ovs-cni-plugin:latest` for the
representor discovery fix; `LOCAL_OVS_CNI_IMAGE` can select a local image to
push instead. `SKIP_TEST=TRUE` provisions without running conformance.
