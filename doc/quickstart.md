# Quickstart Guide

## Prerequisites

1. Kubernetes or Openshift cluster running on bare metal nodes.
2. Multus-cni is deployed as default CNI plugin, and there is a default CNI plugin (flannel, openshift-sdn etc.) available for Multus-cni.

## Installation

Firstly, clone this GitHub repository.

```bash
go get github.com/openshift/sriov-network-operator
```

Install the Operator-SDK. The following commands will put operator-sdk to your $GOPATH/bin, please make sure that path is included in your \$PATH.

```bash
cd $GOPATH/src/github.com/openshift/sriov-network-operator
make operator-sdk
```

Deploy the operator. 

If you are running an Openshift cluster:

```bash
make deploy-setup
```

If you are running a Kubernetes cluster:
```bash
export OPERATOR_EXEC=kubectl
export ENABLE_ADMISSION_CONTROLLER=false
make deploy-setup-k8s
```

By default, the operator will be deployed in namespace 'sriov-network-operator' for Kubernetes cluster, you can check if the deployment is finished successfully.

```bash
$ kubectl get -n sriov-network-operator all
NAME                                          READY   STATUS    RESTARTS   AGE
pod/sriov-network-config-daemon-bf9nt         1/1     Running   0          8s
pod/sriov-network-operator-54d7545f65-296gb   1/1     Running   0          10s

NAME                             TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)    AGE
service/sriov-network-operator   ClusterIP   10.102.53.223   <none>        8383/TCP   9s

NAME                                         DESIRED   CURRENT   READY   UP-TO-DATE   AVAILABLE   NODE SELECTOR                                                 AGE
daemonset.apps/sriov-network-config-daemon   1         1         1       1            1           beta.kubernetes.io/os=linux,node-role.kubernetes.io/worker=   8s

NAME                                     READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/sriov-network-operator   1/1     1            1           10s

NAME                                                DESIRED   CURRENT   READY   AGE
replicaset.apps/sriov-network-operator-54d7545f65   1         1         1       10s
```

You may need to label SR-IOV worker nodes using `node-role.kubernetes.io/worker` label, if not already.

**Note:** By default, SR-IOV Operator will be deployed in namespace 'openshift-sriov-network-operator' for OpenShift cluster.

## Configuration

After the operator gets installed, you can configure it with creating the custom resource of SriovNetwork and SriovNetworkNodePolicy. But before that, you may want to check the status of SriovNetworkNodeState CRs to find out all the SRIOV capable devices in you cluster.

Here comes an example. As you can see, there are 2 SR-IOV NICs from Intel.

```bash
$ oc get sriovnetworknodestates.sriovnetwork.openshift.io -n sriov-network-operator node-1 -o yaml

apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodeState
spec: ...
status:
  interfaces:
  - deviceID: "1572"
    driver: i40e
    mtu: 1500
    pciAddress: "0000:18:00.0"
    totalvfs: 64
    vendor: "8086"
  - deviceID: "1572"
    driver: i40e
    mtu: 1500
    pciAddress: "0000:18:00.1"
    totalvfs: 64
    vendor: "8086"
```

You can choose the NIC you want when creating SriovNetworkNodePolicy CR, by specifying the 'nicSelector'.

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: policy-1
  namespace: sriov-network-operator
spec:
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  resourceName: intelnics
  priority: 99
  mtu: 9000
  numVfs: 2
  nicSelector:
      deviceID: "1572"
      rootDevices:
      - 0000:18:00.1
      vendor: "8086"
  deviceType: netdevice
```

After applying your SriovNetworkNodePolicy CR, check the status of SriovNetworkNodeState again, you should be able to see the NIC has been configured as instructed.

```bash
$ oc get sriovnetworknodestates.sriovnetwork.openshift.io -n sriov-network-operator node-1 -o yaml

...
- Vfs:
    - deviceID: 1572
      driver: iavf
      pciAddress: 0000:18:02.0
      vendor: "8086"
    - deviceID: 1572
      driver: iavf
      pciAddress: 0000:18:02.1
      vendor: "8086"
    - deviceID: 1572
      driver: iavf
      pciAddress: 0000:18:02.2
      vendor: "8086"
    deviceID: "1583"
    driver: i40e
    mtu: 1500
    numVfs: 3
    pciAddress: 0000:18:00.0
    totalvfs: 64
    vendor: "8086"
...
```

At the same time, the SRIOV device plugin and CNI plugin has been provisioned to the worker node. You may check if resource name 'intel-nics' is reported  by device plugin correctly.

```bash
$ kubectl get no -o json | jq -r '[.items[] | {name:.metadata.name, allocable:.status.allocatable}]'
[
  {
    "name": "minikube",
    "allocable": {
      "cpu": "72",
      "ephemeral-storage": "965895780801",
      "hugepages-1Gi": "0",
      "hugepages-2Mi": "0",
      "intel.com/intel-nics": "3",
      "memory": "196706684Ki",
      "openshift.io/sriov": "0",
      "pods": "110"
    }
  }
]
```

Now you can create a SriovNetwork CR which refer to the 'resourceName' defined in SriovNetworkNodePolicy. Then a NetworkAttachementDefinition CR will be generated by operator with the same name and namespace. 

Here is an example:

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetwork
metadata:
  name: example-sriovnetwork
  namespace: sriov-network-operator
spec:
  ipam: | 
    {
      "type": "host-local",
      "subnet": "10.56.217.0/24",
      "rangeStart": "10.56.217.171",
      "rangeEnd": "10.56.217.181",
      "routes": [{
        "dst": "0.0.0.0/0"
      }],
      "gateway": "10.56.217.1"
    }
  vlan: 0
  resourceName: intelnics
```

To remove the operator related resources.

If you are running an Openshift cluster:

```bash
make undeploy
```

If you are running a Kubernetes cluster:

```bash
make undeploy-k8s
```

## Hack

To run the operator locally.

````bash
make run
````

To run the e2e test.

```bash
make test-e2e
```

To build the binary.

```bash
make build
```

If you want to test changes to the `network config daemon`, you must:
- build and tag an image locally with `docker build -f Dockerfile.sriov-network-config-daemon -t imagename`
- push the image to a registry
- change `hack/env.sh` value for `SRIOV_NETWORK_CONFIG_DAEMON_IMAGE` pointing _imagename_ from the registry you pushed the image to

and then `make run`
