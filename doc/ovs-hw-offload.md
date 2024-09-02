# OVS Hardware Offload

The OVS software based solution is CPU intensive, affecting system performance
and preventing fully utilizing available bandwidth. OVS 2.8 and above support
a feature called OVS Hardware Offload which improves performance significantly.
This feature allows offloading the OVS data-plane to the NIC while maintaining
OVS control-plane unmodified. It is using SR-IOV technology with VF representor
host net-device. The VF representor plays the same role as TAP devices
in Para-Virtual (PV) setup. A packet sent through the VF representor on the host
arrives to the VF, and a packet sent through the VF is received by its representor.

OVS Hardware Offloading requires NIC to be configured in `switchdev` mode.

The operator can automate the creation and configuration of OVS bridges when the "manageSoftwareBridges" featureGate is activated.

## Supported Ethernet controllers

The following manufacturers are known to work:

- Mellanox ConnectX-5 and above

## Prerequisites

- OpenVswitch installed

### Activate "manageSoftwareBridges" featureGate

```
kubectl patch sriovoperatorconfigs.sriovnetwork.openshift.io -n network-operator default --patch '{ "spec": { "featureGates": { "manageSoftwareBridges": true  } } }' --type='merge'
```


### Deploy SriovNetworkNodePolicy

The example policy below selects all NVIDIA ConnectX-6 Dx devices on all worker nodes.

The following actions will apply to selected NICs:

* set NIC's eswitch mode to `switchdev`

* create 5 Virtual Functions on each Physical Function

* create a separate OVS bridge (with default configuration that is suitable for HW-offloading with OVS-kernel dataplane) for each Physical Function (PF)

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: ovs-hw-offload
  namespace: sriov-network-operator
spec:
  eSwitchMode: switchdev
  mtu: 1500
  nicSelector:
    deviceID: 101d
    vendor: 15b3
  nodeSelector:
    node-role.kubernetes.io/worker: ""
  numVfs: 5
  resourceName: switchdev
  bridge:
    ovs: {}
```

_Note: `spec.bridge.ovs: {}` - means use default settings (suitable for HW-offloading with OVS-kernel dataplane)
The fields defined in the [Bridge type](https://github.com/k8snetworkplumbingwg/sriov-network-operator/blob/master/api/v1/sriovnetworknodepolicy_types.go#L84) can be used to configure advanced bridge and interface level options._

The spec above will render to the similar SriovNetworkNodeState for matching nodes.

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodeState
metadata:
  name: node-a
  namespace: sriov-network-operator
spec:
  bridges:
    ovs:
    - bridge: {}
      name: br-0000_d8_00.0
      uplinks:
      - interface: {}
        name: enp216s0f0np0
        pciAddress: 0000:d8:00.0
    - bridge: {}
      name: br-0000_d8_00.1
      uplinks:
      - interface: {}
        name: enp216s0f1np1
        pciAddress: 0000:d8:00.1
  interfaces:
  - eSwitchMode: switchdev
    mtu: 1500
    name: enp216s0f0np0
    numVfs: 5
    pciAddress: 0000:d8:00.0
    vfGroups:
    - deviceType: netdevice
      mtu: 1500
      policyName: ovs-hw-offload
      resourceName: switchdev
      vfRange: 0-4
  - eSwitchMode: switchdev
    mtu: 1500
    name: enp216s0f1np1
    numVfs: 5
    pciAddress: 0000:d8:00.1
    vfGroups:
    - deviceType: netdevice
      mtu: 1500
      policyName: ovs-hw-offload
      resourceName: switchdev
      vfRange: 0-4
```

In this example node-a has single ConnectX-6 Dx card with two ports (two Physical Functions).
For each Physical Function a separate OVS bridge will be created.

PF `0000:d8:00.0` -> OVS-bridge `br-0000_d8_00.0`
PF `0000:d8:00.1` -> OVS-bridge `br-0000_d8_00.1`

The PCI address of the PF is used to generate a predictable name for the bridge.

The PFs will be automatically attached to the bridges.


### Create kind: OVSNetwork CR

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: OVSNetwork
metadata:
  name: ovs
  namespace: sriov-network-operator
spec:
  networkNamespace: default
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
  resourceName: switchdev
  vlan: 200
```

_Note: There is no need to explicitly specify bridge name in the OVSNetwork. The `ovs-cni` will be able to automatically select the right OVS bridge based on the allocated VF for the Pod._

### Deploy POD with OVS hardware-offload

Create POD spec and request a VF

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: ovs-offload-pod1
  annotations:
    k8s.v1.cni.cncf.io/networks: ovs
spec:
  containers:
  - name: ovs-offload
    image: networkstatic/iperf3
    resources:
      requests:
        openshift.io/switchdev: '1'
      limits:
        openshift.io/switchdev: '1'
    command:
    - sh
    - -c
    - |
      ls -l /dev/infiniband /sys/class/net
      sleep 1000000
```

## Verify Hardware-Offloads is Working

Run iperf3 server on POD 1

```bash
kubectl exec -it ovs-offload-pod1 -- iperf3 -s
```

Run iperf3 client on POD 2

```bash
kubectl exec -it ovs-offload-pod2 -- iperf3 -c 192.168.1.17 -t 100
```

Check traffic on the VF representor port. Verify only TCP connection establishment appears

```text
tcpdump -i enp3s0f0_3 tcp
listening on enp3s0f0_3, link-type EN10MB (Ethernet), capture size 262144 bytes
22:24:44.969516 IP 192.168.1.16.43558 > 192.168.1.17.targus-getdata1: Flags [S], seq 89800743, win 64860, options [mss 1410,sackOK,TS val 491087056 ecr 0,nop,wscale 7], length 0
22:24:44.969773 IP 192.168.1.17.targus-getdata1 > 192.168.1.16.43558: Flags [S.], seq 1312764151, ack 89800744, win 64308, options [mss 1410,sackOK,TS val 4095895608 ecr 491087056,nop,wscale 7], length 0
22:24:45.085558 IP 192.168.1.16.43558 > 192.168.1.17.targus-getdata1: Flags [.], ack 1, win 507, options [nop,nop,TS val 491087222 ecr 4095895608], length 0
22:24:45.085592 IP 192.168.1.16.43558 > 192.168.1.17.targus-getdata1: Flags [P.], seq 1:38, ack 1, win 507, options [nop,nop,TS val 491087222 ecr 4095895608], length 37
22:24:45.086311 IP 192.168.1.16.43560 > 192.168.1.17.targus-getdata1: Flags [S], seq 3802331506, win 64860, options [mss 1410,sackOK,TS val 491087279 ecr 0,nop,wscale 7], length 0
22:24:45.086462 IP 192.168.1.17.targus-getdata1 > 192.168.1.16.43560: Flags [S.], seq 441940709, ack 3802331507, win 64308, options [mss 1410,sackOK,TS val 4095895725 ecr 491087279,nop,wscale 7], length 0
22:24:45.086624 IP 192.168.1.16.43560 > 192.168.1.17.targus-getdata1: Flags [.], ack 1, win 507, options [nop,nop,TS val 491087279 ecr 4095895725], length 0
22:24:45.086654 IP 192.168.1.16.43560 > 192.168.1.17.targus-getdata1: Flags [P.], seq 1:38, ack 1, win 507, options [nop,nop,TS val 491087279 ecr 4095895725], length 37
22:24:45.086715 IP 192.168.1.17.targus-getdata1 > 192.168.1.16.43560: Flags [.], ack 38, win 503, options [nop,nop,TS val 4095895725 ecr 491087279], length 0
```

Check datapath rules are offloaded

```text
ovs-appctl dpctl/dump-flows --names type=offloaded
recirc_id(0),in_port(eth0),eth(src=16:fd:c6:0b:60:52),eth_type(0x0800),ipv4(src=192.168.1.17,frag=no), packets:2235857, bytes:147599302, used:0.550s, actions:ct(zone=65520),recirc(0x18)
ct_state(+est+trk),ct_mark(0),recirc_id(0x18),in_port(eth0),eth(dst=42:66:d7:45:0d:7e),eth_type(0x0800),ipv4(dst=192.168.1.0/255.255.255.0,frag=no), packets:2235857, bytes:147599302, used:0.550s, actions:eth1
recirc_id(0),in_port(eth1),eth(src=42:66:d7:45:0d:7e),eth_type(0x0800),ipv4(src=192.168.1.16,frag=no), packets:133410141, bytes:195255745684, used:0.550s, actions:ct(zone=65520),recirc(0x16)
ct_state(+est+trk),ct_mark(0),recirc_id(0x16),in_port(eth1),eth(dst=16:fd:c6:0b:60:52),eth_type(0x0800),ipv4(dst=192.168.1.0/255.255.255.0,frag=no), packets:133410138, bytes:195255745483, used:0.550s, actions:eth0
```
