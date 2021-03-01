## E2E test with KinD
### How to test

```
$ git clone https://github.com/k8snetworkplumbingwg/sriov-network-operator.git
$ cd sriov-network-operator/
$ ./hack/get-e2e-kind-tools.sh
$ ./hack/test-e2e-kind-cluster.sh ${PF_PCI_ADDRESS}
```

### How to teardown

```
$ ./hack/teardown-e2e-kind-cluster.sh
```
