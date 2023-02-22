# VDPA

Virtual data path acceleration (vDPA) in essence is an approach to standardize 
the NIC SRIOV data plane using the virtio ring layout and placing a single standard 
virtio driver in the guest/pod. It’s decoupled from any vendor implementation while 
adding a generic control plane and SW infrastructure to support it. Given that it’s 
an abstraction layer on top of SRIOV (Single Root I/O Virtualization) it is also 
future proof to support emerging technologies such as scalable IOV.

Main aspects of this solution:
- primary interface in the pod is configured as a virtio/vDPA device
- OVS HW offload enabled
- NIC configured in switchdev mode
- OVN-kubernetes CNI

## Supported Ethernet controllers

The following manufacturers/NICs are known to work:

- Mellanox ConnectX-6 Dx

## Prerequisites

- OpenVswitch installed
- Network Manager installed

### Deploy SriovNetworkNodePolicy

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: vdpa-policy
  namespace: openshift-sriov-network-operator
spec:
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  resourceName: mlxnics
  priority: 10
  numVfs: 2
  nicSelector:
    vendor: "15b3"
    deviceID: "101d"
    rootDevices:
    - 0000:65:00.0
    - 0000:65:00.1
  deviceType: netdevice
  eSwitchMode: switchdev
  vdpaType: virtio
```

### Create NetworkAttachementDefinition CRD with OVN-K CNI config

```yaml
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: ovn-kubernetes-a
  namespace: kube-system
  annotations:
    k8s.v1.cni.cncf.io/resourceName: openshift.io/mlxnics
spec:
  config: '{
      "cniVersion": "0.3.1",
      "name":"ovn-kubernetes-a",
      "type": "ovn-k8s-cni-overlay",
      "ipam": {},
      "dns": {}
    }'
```

### Deploy POD with virtio/vDPA

Create POD spec and request a VF

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: vdpa-pod1
  namespace: vdpa
  annotations:
    v1.multus-cni.io/default-network: kube-system/ovn-kubernetes-a
spec:
  containers:
  - name: vdpa-pod
    image: networkstatic/iperf3
    imagePullPolicy: IfNotPresent
    securityContext:
      privileged: true
    command:
      - sleep
      - "3600"
```

## Verify vDPA is Working

Run ethtool to verify the correctness of the virtio/vDPA interface in the pod:

```bash
kubectl exec -it vdpa-pod1 -- ethtool -i eth0
```

```text
driver: virtio_net
version: 1.0.0
firmware-version: 
expansion-rom-version: 
bus-info: vdpa:0000:65:00.4
supports-statistics: yes
supports-test: no
supports-eeprom-access: no
supports-register-dump: no
supports-priv-flags: no
```

Check connectivity between the pods:

```bash
kubectl exec -it vdpa-pod1 -- ping <other-pod-ip-address>
```

Check datapath rules are offloaded

```text
ovs-appctl dpctl/dump-flows -m type=offloaded
ufid:30ae9875-d107-46a1-b8cb-cdaafd89d440, skb_priority(0/0),skb_mark(0/0),ct_state(0/0),ct_zone(0/0),ct_mark(0/0),ct_label(0/0),recirc_id(0),dp_hash(0/0),in_port(ac63a8e99747603),packet_type(ns=0/0,id=0/0),eth(src=0a:58:c0:a8:00:06,dst=0a:58:c0:a8:00:05),eth_type(0x0800),ipv4(src=192.168.0.6,dst=192.168.0.5,proto=6,tos=0/0,ttl=0/0,frag=no),tcp(src=0/0,dst=57426), packets:7, bytes:396, used:2.310s, offloaded:yes, dp:tc, actions:ct(zone=15,nat),recirc(0x440)
ufid:b895ff1d-4085-4199-8b49-0b6fc9e54a29, skb_priority(0/0),skb_mark(0/0),ct_state(0/0),ct_zone(0/0),ct_mark(0/0),ct_label(0/0),recirc_id(0),dp_hash(0/0),in_port(ac63a8e99747603),packet_type(ns=0/0,id=0/0),eth(src=0a:58:c0:a8:00:06,dst=0a:58:c0:a8:00:05),eth_type(0x0800),ipv4(src=192.168.0.6,dst=192.168.0.5,proto=6,tos=0/0,ttl=0/0,frag=no),tcp(src=0/0,dst=57428), packets:46244, bytes:3052518, used:0.260s, offloaded:yes, dp:tc, actions:ct(zone=15,nat),recirc(0x442)
ufid:ea3e4350-70ac-46aa-8fba-fff5ee54dc32, skb_priority(0/0),skb_mark(0/0),ct_state(0/0),ct_zone(0/0),ct_mark(0/0),ct_label(0/0),recirc_id(0),dp_hash(0/0),in_port(ac63a8e99747603),packet_type(ns=0/0,id=0/0),eth(src=0a:58:c0:a8:00:06,dst=00:00:00:00:00:00/00:00:00:00:00:00),eth_type(0x86dd),ipv6(src=::/::,dst=::/::,label=0/0,proto=0/0,tclass=0/0,hlimit=0/0,frag=no), packets:0, bytes:0, used:6.270s, offloaded:yes, dp:tc, actions:drop
ufid:a23365ae-26cd-4bbb-85b5-4299c05a350d, skb_priority(0/0),skb_mark(0/0),ct_state(0/0),ct_zone(0/0),ct_mark(0/0),ct_label(0/0),recirc_id(0),dp_hash(0/0),in_port(8630f7a90005cdd),packet_type(ns=0/0,id=0/0),eth(src=0a:58:c0:a8:00:05,dst=0a:58:c0:a8:00:06),eth_type(0x0800),ipv4(src=192.168.0.5,dst=192.168.0.6,proto=6,tos=0/0,ttl=0/0,frag=no),tcp(src=0/0,dst=5201), packets:2156610, bytes:3049431372, used:0.260s, offloaded:yes, dp:tc, actions:ct(zone=14,nat),recirc(0x43e)
```
