## E2E test with KinD
Kubernetes IN Docker (KIND) is a tool to deploy Kubernetes inside Docker containers. It is used to test multi nodes scenarios on a single baremetal node.
To run the E2E tests inside a KIND cluster, `./hack/run-e2e-test-kind.sh` can be used. The script performs the following operations:

 * Deploys a 2 node KIND cluster (master and worker)
 * Moves the specified SR-IOV capable PCI net device to KIND worker namespace
 * Deploys the operator
 * Runs E2E tests

There are two modes of moving the specified SR-IOV capable PCI net device to the KIND worker namespace:

 * `test-suite` (default): In this mode, the E2E test suite handle the PF and its VFs switching to the test namespace.
 * `system-service` mode: In this mode a dedicated system service is used to switch the PF and VFs to the test namespace.

The mode can be selected using the `INTERFACES_SWITCHER` environment variable, or by passing the mode to the `./hack/run-e2e-test-kind.sh` script using the `--device-netns-switcher` flag.

### How to test
To execute E2E tests, a SR-IOV Physical Function device is required and will be added to a KinD workers network namespace, depending on the device netns switcher method, the testing steps can defer.  

Note: Test device will remain in KinD worker node until cluster is terminated.

#### Device netns switcher mode `test-suite`
```
$ git clone https://github.com/k8snetworkplumbingwg/sriov-network-operator.git
$ cd sriov-network-operator/
$ source hack/get-e2e-kind-tools.sh
$ export TEST_PCI_DEVICE=0000:02:00.0
$ sudo ./hack/run-e2e-test-kind.sh --pf-pci-address $TEST_PCI_DEVICE
```

#### Device netns switcher mode `system-service`
The `system-service` mode uses a linux service to handle the interface switching. To prepare the service, the following needs to be done as root:
```
cp ./hack/vf-netns-switcher.sh /usr/bin/
cp ./hack/vf-switcher.service /etc/systemd/system/
systemctl daemon-reload
```
For the service to work properly the `jq` tool is needed.

To run the E2E tests do:
```
$ git clone https://github.com/k8snetworkplumbingwg/sriov-network-operator.git
$ cd sriov-network-operator/
$ source hack/get-e2e-kind-tools.sh
$ KUBECONFIG=/etc/kubernetes/admin.conf
$ INTERFACES_SWITCHER=system-service
$ ./hack/run-e2e-test-kind.sh --pf-pci-address <interface pci>
```

### How to repeat test using existing KinD cluster
Export test PCI device used to set up KinD cluster and export KinD worker network namespace path:
```
$ export KUBECONFIG="${HOME}/.kube/config"
$ export TEST_PCI_DEVICE=0000:02:00.0
$ export TEST_NETNS_PATH=$(docker inspect "$(docker ps --filter 'name=kind-worker' -q)" --format "{{ .NetworkSettings.SandboxKey }}")
$ sudo make test-e2e-k8s
```

### How to teardown

```
$ ./hack/teardown-e2e-kind-cluster.sh
```

#### Cleaning up the `system-service` service files
```
$ sudo rm -f /etc/systemd/system/vf-switcher.service
$ sudo rm -f /usr/bin/vf-netns-switcher.sh
$ sudo systemctl daemon-reload
```

### Known limitations / issues
* Webhooks are disabled by default when testing
