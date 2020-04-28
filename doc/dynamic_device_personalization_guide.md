# Intel® Dynamic Device Personalization
Dynamic Device Personalization aka DDP allows dynamic reconfiguration of the packet processing pipeline of Intel Ethernet 700 Series to meet use case needs.

[For more information about Dynamic Device Personalization for Intel® Ethernet 700 Series](https://software.intel.com/en-us/articles/dynamic-device-personalization-for-intel-ethernet-700-series)

## Support in SR-IOV Network Operator
A new field ```ddpUrl``` is created in ```SriovNetworkNodePolicy``` custom resource to reference a DDP package from a remote location using a URL which will allow adding new packet processing pipeline configuration profiles to a network adapter at run time, without resetting or rebooting the server.

```yaml
---
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovNetworkNodePolicy
metadata:
  name: vBNG_optimised_policy
  namespace: sriov-network-operator
spec:
  resourceName: intel_ddp_vbng
  nodeSelector:
    feature.node.kubernetes.io/network-sriov.capable: "true"
  priority: 99
  ddpUrl: "https://server:8000/ddp_profiles/pppoe/v1_0_0_0/ppp-oe-ol2tpv2.zip"
  mtu: 1500
  numVfs: 4
  nicSelector:
    vendor: "8086"
    rootDevices: ['0000:02:00.0']
  deviceType: vfio-pci
```
SR-IOV Network Operator will fetch the DDP package and apply the profile to NICs, as selected by the ```nicSelector```.
Note: ```rootDevices`` must contain only root physical functions.

### Available DDP packages
The following table describes some of the available DDP packages for X700 series NICs.

| Zip name              | Description                                                                                                                                                                                                                                       |
|--------------------   |------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------   |
| l2tpv3oip-l4.zip      | The DDP profile contains the X710/XXV710/XL710 parser graph for L2TPv3 over IPv4                                                                                                                                                                  |
| radiofh4g.zip         | This Classification offload is required for FlexRAN fronthaul to enable filtering of data packets based on their unique subtype (Timing packets, PUSCH and PRACH packets)                                                                         |
| ipv4-mcast.zip        | The IPv4 Multicast profile can be used to enhance performance and optimized core utilization for virtual network functions extensively processing IPv4 multicast traffic requiring separation of multicast and unicast IPv4 flows.                |
| mplsogreudp.zip       | The MPLS Profile enables MPLSoGRE/MPLSoUDP classification for MPLS tunnels with Ethernet frames payload.                                                                                                                                          |
| gtp.zip               | The GTPv1 profile can be used to enhance performance and optimize core utilization for virtualized enhanced packet core (vEPC) and multi-access edge computing (MEC) use cases.                                                                   |
| ppp-oe-ol2tpv2.zip    | The PPPoE profile can be used to enhance performance and optimize core utilization for virtual broadband network gateway (vBNG) and virtual broadband remote access server (vBRAS) use cases, where PPPoE is the dominant data plane protocol.    |
| ecpri.zip             | The eCPRI profile supports eCPRI over Ethernet and IPv4/UDP protocols.                                                                                                                                                                            |
| esp-ah.zip            | The IP Sec profile can be used to enhance performance and optimized core utilization for IPSec packets with unique protocol IDs for IP Encapsulating Security Payload (ESP ) and IP Authentication Header (AH).                                   |

### Download DDP packages
Full list of DDP packages and their associated zip files are available from [Intel download center](https://downloadcenter.intel.com/search?keyword=personalization).

It is recommended to self host DDP packages and reference it in ```ddpUrl```. It is also recommended to include the DDP package version in the URL which references the DDP package.
For example:
```
ddpUrl: https://myserver:8000/ddp-profiles/x700/pppoe/v1-0-0-0/ppp-oe-ol2tpv2.zip
```
This will help to inspect the DDP package version by just looking at the CR. You can also confirm DDP profile version by inspecting ```SriovNetworkNodeState``` once you have applied a policy with a defined ```ddpUrl```.

```bash
$ oc describe sriovnetworknodestate node1 -n ${Operator namespace}
    ...
    Ddp:            GTPv1-C/U IPv4/IPv6 payload - v1.0.3.0
    Device ID:      1572
    Driver:         i40e
    ...
```

### Operator DDP profile/package caching and force upgrade
Each of Operator's host daemons will download the DDP package as referenced in ```ddpUrl``` only once unless a change to the URL ```ddpUrl``` or Operator build version. This can facilitate upgrade of DDP profiles.

An example upgrade of a DDP package ppp-oe will involve updating ```ddpUrl``` from:
```
ddpUrl: https://myserver:8000/ddp_profiles/x700/pppoe_v1_0_0_0/ppp-oe-ol2tpv2.zip
```
to
```
ddpUrl: https://myserver:8000/ddp_profiles/x700/pppoe_v1_0_0_1/ppp-oe-ol2tpv2.zip
```
A host which is managed by this Operator can reboot and not require the DDP package to be fetched again from the remote URL.

### Viewing DDP specific logs or debugging issues
You can view extensive logging of this DDP component by inspecting the logs of the SR-IOV Operator daemon:
```bash
$ oc logs ${Operator daemon name} -n ${Operator namespace} | grep "intel-plugin"
```

## Prerequisites
 * Firmware: v6.01 or newer
 * Hardware: X700 series NIC
 * Driver: i40e v2.7.26 or newer

Refer to [Intel download center](https://downloadcenter.intel.com/) for latest firmware and drivers for Intel Ethernet 700 Series.

You can use `ethtool` to get driver and firmware information.

```bash
$ ethtool -i enp2s0f0
driver: i40e
version: 2.9.21
firmware-version: 7.00 0x80004cda 1.2154.0
expansion-rom-version:
bus-info: 0000:02:00.0
supports-statistics: yes
supports-test: yes
supports-eeprom-access: yes
supports-register-dump: yes
supports-priv-flags: yes
```
