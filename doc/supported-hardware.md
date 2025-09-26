# Supported Hardware

The following SR-IOV capable hardware is supported with sriov-network-operator:

| Model                    | Vendor ID | Device ID |
| ------------------------ | --------- | --------- |
| Intel XXV710 Family |  8086 | 158b |
| Intel X710 10GbE Backplane | 8086 | 1581 |
| Intel X710 10GbE Base T | 8086 | 15ff |
| Intel X550 Family | 8086 | 1563 |
| Intel X557 Family |  8086 | 1589 |
| Intel E810 Family | 8086  | 1591 |
| Intel E810-CQDA2/2CQDA2 Family | 8086  | 1592 |
| Intel E810-XXVDA4 Family | 8086  | 1593 |
| Intel E810-XXVDA2 Family | 8086  | 159b |
| Intel E810-XXV Backplane Family | 8086  | 1599 |
| Intel E823-C Family | 8086  | 188a |
| Intel E823-L SFP Family | 8086  | 124d |
| Intel E823-L Backplane Family | 8086  | 124c |
| Intel E825-C Backplane Family | 8086 | 579c |
| Intel E825-C QSFP Family | 8086 | 579d |
| Intel E825-C SFP Family | 8086 | 579e |
| Intel E830-CC QSFP Family | 8086 | 12d2 |
| Intel E830-CC SFP Family | 8086 | 12d3 |
| Intel E835-CC QSFP Family | 8086 | 1249 |
| Intel E835-CC SFP Family | 8086 | 124a |
| Mellanox MT27700 Family [ConnectX-4] | 15b3 | 1013 |
| Mellanox MT27710 Family [ConnectX-4 Lx] | 15b3 | 1015 |
| Mellanox MT27800 Family [ConnectX-5] | 15b3 | 1017 |
| Mellanox MT28800 Family [ConnectX-5 Ex] | 15b3 | 1019 |
| Mellanox MT28908 Family [ConnectX-6] | 15b3 | 101b |
| Mellanox MT28908 Family [ConnectX-6 Dx] | 15b3 | 101d |
| Mellanox MT28908 Family [ConnectX-6 Lx] | 15b3 | 101f |
| Mellanox MT2910 Family [ConnectX-7] | 15b3 | 1021 |
| Mellanox CX8 Family [ConnectX-8] | 15b3 | 1023 |
| Mellanox MT42822 BlueField-2 integrated ConnectX-6 Dx | 15b3 | a2d6 |
| Mellanox MT43244 BlueField-3 integrated ConnectX-7 Dx | 15b3 | a2dc |
| Qlogic QL45000 Series 50GbE Controller | 1077 | 1654 |
| Marvell OCTEON TX2 CN96XX | 177d | b200 |
| Marvell OCTEON TX2 CN98XX | 177d | b100 |
| Marvell OCTEON Fusion CNF95XX | 177d | b600 |
| Marvell OCTEON 10 CN10XXX | 177d | b900 |
| Marvell OCTEON Fusion CNF105XX | 177d | ba00 |

> **Note:** sriov-network-operator maintains a list of supported NICs which it supports.
> These are stored in supported-nic-ids [configMap](https://github.com/k8snetworkplumbingwg/sriov-network-operator/blob/master/deployment/sriov-network-operator/templates/configmap.yaml).
> The operator uses this list to enforce it only operates on NICs that are supported. For unsupported SR-IOV NICs, that is not guaranteed, but might work as well.
> To have sriov-network-operator operate on an unsupported NIC, after installing the operator, you have to add the unsupported SR-IOV NICs information to the ConfigMap
> in following format: `<nic_name>: <vender_id> <pf_device_id> <vf_device_id>`.
> Then restart the config daemon and operator webhook pods.

## Supported features per hardware

The following table depicts the supported SR-IOV hardware features of each supported hardware:

| Model                    | SR-IOV Kernel | SR-IOV DPDK | SR-IOV Hardware Offload (switchdev) |
| ------------------------ | ------------- | ----------- |------------------------------------ |
| Intel XXV710 Family | V | V | X |
| Intel X710 10GbE Backplane | V | V | X |
| Intel X710 10GbE Base T    | V | V | X |
| Intel X550 Family | V | V | X |
| Intel X557 Family | V | V | X |
| Intel E810 Family | V | V | X |
| Intel E810-CQDA2/2CQDA2 Family | V | V | X |
| Intel E810-XXVDA4 Family | V | V | X |
| Intel E810-XXVDA2 Family | V | V | X |
| Intel E810-XXV Backplane Family | V | V | X |
| Intel E823-C Family | V | V | X |
| Intel E823-L SFP Family | V | V | X |
| Intel E823-L Backplane Family | V | V | X |
| Intel E825-C Backplane | V | V | X |
| Intel E825-C QSFP Family | V | V | X |
| Intel E825-C SFP Family | V | V | X |
| Intel E830-CC QSFP Family | V | V | X |
| Intel E830-CC SFP Family | V | V | X |
| Intel E835-CC QSFP Family | V | V | X |
| Intel E835-CC SFP Family | V | V | X |
| Mellanox MT27700 Family [ConnectX-4] | V | V | V |
| Mellanox MT27710 Family [ConnectX-4 Lx] | V | V | V |
| Mellanox MT27800 Family [ConnectX-5] | V | V | V |
| Mellanox MT28800 Family [ConnectX-5 Ex] | V | V | V |
| Mellanox MT28908 Family [ConnectX-6] | V | V | V |
| Mellanox MT28908 Family [ConnectX-6 Dx] | V | V | V |
| Mellanox MT28908 Family [ConnectX-6 Lx] | V | V | V |
| Mellanox MT28908 Family [ConnectX-7] | V | V | V |
| Mellanox CX8 Family [ConnectX-8] | V | V | V |
| Mellanox MT42822 BlueField-2 integrated ConnectX-6 Dx | V | V | V |
| Mellanox MT43244 BlueField-3 integrated ConnectX-6 Dx | V | V | V |
| Qlogic QL45000 Series 50GbE Controller | V | X | X |
| Marvell OCTEON TX2 CN96XX | V | V | X |
| Marvell OCTEON TX2 CN98XX | V | V | X |
| Marvell OCTEON Fusion CNF95XX | V | V | X |
| Marvell OCTEON 10 CN10XXX | V | V | X |
| Marvell OCTEON Fusion CNF105XX | V | V | X |
| Red_Hat_Virtio_network_device | X | V | X |
| Red_Hat_Virtio_1_0_network_device | X | V | X |

# Adding new Hardware

## Initial support
Vendors (or other parties) interested in adding a new supported hardware to the supported list of devices of sriov-network-operator
should follow the following procedure:

* Open an Issue requesting to add support for new hardware
  * Specify which hardware it is (Model, Vendor ID, Device ID)
* Perform Testing on requested hardware using:
  * Kubernetes last release
  * Either last release version or master version of sriov-network-operator
  * Note: We do not have a specifc list of test-cases however you should at a minimum ensure
      sriov-network-operator is able to properly discover your device, configure SR-IOV and
      expose them as a kubernetes node resource, and you are able to run workloads consuming
      those resources.
* Add information of what was tested to the issue opened
* Add contact point information to [vendor-support.md](https://github.com/k8snetworkplumbingwg/sriov-network-operator/blob/master/doc/vendor-support.md), so we know who to reach out if issues arise when running sriov-network-operator against the specified hardware.
* Submit PR to add your device to this file as well as to supported-nic-ids configMap [here](https://github.com/k8snetworkplumbingwg/sriov-network-operator/blob/master/deployment/sriov-network-operator/templates/configmap.yaml) and [here](https://github.com/k8snetworkplumbingwg/sriov-network-operator/blob/master/deploy/configmap.yaml).
  * The tables above should be updated according to what was tested

## Continuous support
To ensure sriov-network-operator continues to operate as expected on supported hardware it is recommended that hardware vendors (or another party)
adds CI which runs against PRs in the project. Without it we cannot commit for sriov-network-operator to continue to work properly on the specified
hardware.

Additional information on how to add Vendor/3rd-party CI can be found [here](https://github.com/k8snetworkplumbingwg/sriov-network-operator/tree/master/ci).

### E2E tests

sriov-network-operator offers a set of e2e tests vendors can run on their hardware. These tests utilize [Kind](https://kind.sigs.k8s.io/) to spin up
a Kubernetes cluster and run tests. Information on how to run e2e tests can be found [here](https://github.com/k8snetworkplumbingwg/sriov-network-operator/blob/master/doc/testing-kind.md).
These tests may be used as part of vendor CIs added to the project to validate sriov-network-operator is able to operate
on the relevant hardware.

>**Note:** The maintainers of this project reserve the right to remove a device from the supported list if issues arise on that hardware.
> this will be done only after attempting to contact the contact point provided by a specific vendor to address the issues.
