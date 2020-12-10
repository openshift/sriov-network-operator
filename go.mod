module github.com/k8snetworkplumbingwg/sriov-network-operator

go 1.13

require (
	cloud.google.com/go v0.58.0 // indirect
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/Masterminds/sprig v2.22.0+incompatible
	github.com/blang/semver v3.5.0+incompatible
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/fsnotify/fsnotify v1.4.9
	github.com/go-logr/logr v0.2.1
	github.com/go-logr/zapr v0.2.0 // indirect
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/google/go-cmp v0.5.0
	github.com/huandu/xstrings v1.3.2 // indirect
	github.com/intel/sriov-network-device-plugin v0.0.0-20200924101303-b7f6d3e06797
	github.com/jaypipes/ghw v0.6.1
	github.com/k8snetworkplumbingwg/network-attachment-definition-client v0.0.0-20200626054723-37f83d1996bc
	github.com/mitchellh/copystructure v1.0.0 // indirect
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/openshift/client-go v0.0.0-20200827190008-3062137373b5
	github.com/openshift/machine-config-operator v0.0.1-0.20201023110058-6c8bd9b2915c
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.0.0
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/sys v0.0.0-20200625212154-ddb9806d33ae // indirect
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0
	google.golang.org/genproto v0.0.0-20200610104632-a5b850bcf112 // indirect
	google.golang.org/protobuf v1.25.0 // indirect
	k8s.io/api v0.19.0
	k8s.io/apimachinery v0.19.0
	k8s.io/client-go v0.19.0
	k8s.io/kubectl v0.19.0
	k8s.io/utils v0.0.0-20200729134348-d5654de09c73
	sigs.k8s.io/controller-runtime v0.6.2
)

replace (
	github.com/openshift/api => github.com/openshift/api v0.0.0-20200827090112-c05698d102cf
	k8s.io/kubectl => k8s.io/kubectl v0.19.0
	k8s.io/kubelet => k8s.io/kubelet v0.19.0
)
