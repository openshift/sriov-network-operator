## E2E test with KinD
### How to test
To execute E2E tests, a SR-IOV Physical Function device is required and will be added to a KinD workers network namespace.
```
$ git clone https://github.com/k8snetworkplumbingwg/sriov-network-operator.git
$ cd sriov-network-operator/
$ ./hack/get-e2e-kind-tools.sh
$ export TEST_PCI_DEVICE=0000:02:00.0
$ sudo ./hack/run-e2e-test-kind.sh $TEST_PCI_DEVICE
```
Note: Test device will remain in KinD worker node until cluster is terminated.

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

### Known limitations / issues
* Webhooks are disabled by default when testing
