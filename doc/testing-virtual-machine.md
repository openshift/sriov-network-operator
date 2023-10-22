## E2E conformance test

It's possible to use QEMU to test the SR-IOV operator on a virtual kubernetes/openshift cluster.
Using the IGB model network driver allow to create virtual functions on the virtual system

## How to test

First you will need to enable the `DEV_MODE` via the operator environment variable.
Second step is to add the intel virtual nic to the supported nics configmap.

Another requirement is to load the vfio kernel module with no_iommu configuration. Example systemd:

```
[Unit]
Description=vfio no-iommu
Before=kubelet.service crio.service node-valid-hostname.service

[Service]
# Need oneshot to delay kubelet
Type=oneshot
ExecStart=/usr/bin/bash -c "modprobe vfio enable_unsafe_noiommu_mode=1"
StandardOutput=journal+console
StandardError=journal+console

[Install]
WantedBy=network-online.target
```

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
