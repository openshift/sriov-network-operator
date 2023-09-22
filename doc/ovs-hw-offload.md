# OVS Hardware Offload

The OVS software based solution is CPU intensive, affecting system performance
and preventing fully utilizing available bandwidth. OVS 2.8 and above support
a feature called OVS Hardware Offload which improves performance significantly.
This feature allows offloading the OVS data-plane to the NIC while maintaining
OVS control-plane unmodified. It is using SR-IOV technology with VF representor
host net-device. The VF representor plays the same role as TAP devices
in Para-Virtual (PV) setup. A packet sent through the VF representor on the host
arrives to the VF, and a packet sent through the VF is received by its representor.

## Supported Ethernet controllers

The following manufacturers are known to work:

- Mellanox ConnectX-5 and above

## Instructions for Mellanox ConnectX-5

## Prerequisites

- OpenVswitch installed
- Network Manager installed

### Deploy SriovNetworkNodePolicy

```yaml
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: ovs-hw-offload
  namespace: sriov-network-operator
spec:
  deviceType: netdevice
  nicSelector:
    deviceID: "1017"
    rootDevices:
    - 0000:02:00.0
    - 0000:02:00.1
    vendor: "15b3"
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  numVfs: 8
  priority: 10
  resourceName: cx5_sriov_switchdev
  isRdma: true
  eSwitchMode: switchdev
  linkType: eth
```

### Create NetworkAttachmentDefinition CRD with OVS CNI config

```yaml
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  name: ovs-net
  annotations:
    k8s.v1.cni.cncf.io/resourceName: openshift.io/cx5_sriov_switchdev
spec:
  config: '{
      "cniVersion": "0.3.1",
      "type": "ovs",
      "bridge": "br-sriov0",
      "vlan": 10
    }'
```

### Deploy POD with OVS hardware-offload

Create POD spec and request a VF

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: ovs-offload-pod1
  annotations:
    k8s.v1.cni.cncf.io/networks: ovs-net
spec:
  containers:
  - name: ovs-offload
    image: networkstatic/iperf3
    resources:
      requests:
        openshift.io/cx5_sriov_switchdev: '1'
      limits:
        openshift.io/cx5_sriov_switchdev: '1'
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
