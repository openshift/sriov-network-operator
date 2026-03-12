# Quickstart Guide

## Prerequisites

1. A supported SRIOV hardware on the cluster nodes. Supported models can be found [here](supported-hardware.md).
2. Kubernetes or Openshift cluster running on bare metal nodes.
3. Multus-cni is deployed as default CNI plugin, and there is a default CNI plugin (flannel, ovn-kubernetes etc.) available for Multus-cni.
4. On RedHat Enterprise Linux and Ubuntu operating systems, the `rdma-core` package must be installed to support RDMA resource provisioning. On RedHat CoreOS the package installation is not required.

## Installation

### OpenShift

For OpenShift clusters, you can use the Operator Lifecycle Manager (OLM) or deploy manually:

**Option 1: Using OLM (Recommended)**
```bash
# The operator is available in OperatorHub
# Install through the OpenShift Console or using CLI
```

**Option 2: Manual Deployment**
```bash
# Clone the repository (for development/testing)
git clone https://github.com/k8snetworkplumbingwg/sriov-network-operator.git
cd sriov-network-operator

# Deploy the operator
make deploy-setup
```

### Kubernetes

For Kubernetes clusters, use Helm for easier deployment and management:

#### Prerequisites
- Helm v3 installed
- Kubernetes v1.17+

#### Install Helm (if not already installed)
```bash
curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/master/scripts/get-helm-3
chmod 500 get_helm.sh
./get_helm.sh
```

#### Deploy with Helm

**Using OCI Registry (Recommended)**
```bash
helm install -n sriov-network-operator --create-namespace \
  --set sriovOperatorConfig.deploy=true \
  sriov-network-operator \
  oci://ghcr.io/k8snetworkplumbingwg/sriov-network-operator-chart
```

**Note:** The Helm chart is available as a container image at [ghcr.io/k8snetworkplumbingwg/sriov-network-operator-chart](https://github.com/k8snetworkplumbingwg/sriov-network-operator/pkgs/container/sriov-network-operator-chart)

#### Enable Webhooks (Optional)

By default, the Helm chart disables webhooks. To enable them:

**Option 1: Using cert-manager (Recommended)**
```bash
# Install cert-manager if not already installed
kubectl apply -f https://github.com/jetstack/cert-manager/releases/download/v1.12.0/cert-manager.yaml

# Deploy with cert-manager certificate generation
helm install -n sriov-network-operator --create-namespace \
  --set sriovOperatorConfig.deploy=true \
  --set operator.admissionControllers.enabled=true \
  --set operator.admissionControllers.certificates.certManager.enabled=true \
  --set operator.admissionControllers.certificates.certManager.generateSelfSigned=true \
  sriov-network-operator \
  oci://ghcr.io/k8snetworkplumbingwg/sriov-network-operator-chart
```

**Option 2: Using Pre-created Certificates**
```bash
# Create certificates manually
kubectl create namespace sriov-network-operator
kubectl -n sriov-network-operator create secret tls operator-webhook-cert --cert=cert.pem --key=key.pem
kubectl -n sriov-network-operator create secret tls network-resources-injector-cert --cert=cert.pem --key=key.pem

# Deploy with admission controllers enabled
helm install -n sriov-network-operator \
  --set sriovOperatorConfig.deploy=true \
  --set operator.admissionControllers.enabled=true \
  sriov-network-operator \
  oci://ghcr.io/k8snetworkplumbingwg/sriov-network-operator-chart
```

#### Pod Security Standards

For clusters with Pod Security Admission enabled, label the namespace:
```bash
kubectl label ns sriov-network-operator pod-security.kubernetes.io/enforce=privileged
```

By default, the operator will be deployed in namespace 'sriov-network-operator' for Kubernetes cluster and on 'openshift-sriov-network-operator' for openshift, you can check if the deployment is finished successfully.

```bash
$ kubectl get -n sriov-network-operator all
NAME                                          READY   STATUS    RESTARTS   AGE
pod/sriov-network-config-daemon-bf9nt         1/1     Running   0          8s
pod/sriov-network-operator-54d7545f65-296gb   1/1     Running   0          10s

NAME                             TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)    AGE
service/sriov-network-operator   ClusterIP   10.102.53.223   <none>        8383/TCP   9s

NAME                                         DESIRED   CURRENT   READY   UP-TO-DATE   AVAILABLE   NODE SELECTOR                                                 AGE
daemonset.apps/sriov-network-config-daemon   1         1         1       1            1           kubernetes.io/os=linux,node-role.kubernetes.io/worker=   8s

NAME                                     READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/sriov-network-operator   1/1     1            1           10s

NAME                                                DESIRED   CURRENT   READY   AGE
replicaset.apps/sriov-network-operator-54d7545f65   1         1         1       10s
```

You may need to label SR-IOV worker nodes using `node-role.kubernetes.io/worker` label, if not already.

## Configuration

After the operator gets installed, you can configure it with creating the custom resource of SriovNetwork and SriovNetworkNodePolicy. But before that, you may want to check the status of SriovNetworkNodeState CRs to find out all the SRIOV capable devices in you cluster.

Here comes an example. As you can see, there are 2 SR-IOV NICs from Intel.

```bash
$ kubectl get sriovnetworknodestates.sriovnetwork.openshift.io -n sriov-network-operator node-1 -o yaml

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
$ kubectl get sriovnetworknodestates.sriovnetwork.openshift.io -n sriov-network-operator node-1 -o yaml

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
      "openshift.io/intel-nics": "3",
      "memory": "196706684Ki",
      "pods": "110"
    }
  }
]
```

Now you can create a SriovNetwork CR which refer to the 'resourceName' defined in SriovNetworkNodePolicy. Then a NetworkAttachmentDefinition CR will be generated by operator with the same name and namespace.

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

## Uninstallation

### OpenShift

```bash
make undeploy
```

### Kubernetes

**Using Helm:**
```bash
helm uninstall -n sriov-network-operator sriov-network-operator
```

**Clean up namespace (optional):**
```bash
kubectl delete namespace sriov-network-operator
```
