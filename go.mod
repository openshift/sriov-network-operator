module github.com/k8snetworkplumbingwg/sriov-network-operator

go 1.16

require (
	cloud.google.com/go v0.58.0 // indirect
	github.com/Masterminds/sprig/v3 v3.2.2
	github.com/blang/semver v3.5.1+incompatible
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/coreos/go-systemd/v22 v22.0.0
	github.com/fsnotify/fsnotify v1.4.9
	github.com/go-logr/logr v0.4.0 // indirect
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/google/go-cmp v0.5.2
	github.com/hashicorp/go-retryablehttp v0.7.0
	github.com/huandu/xstrings v1.3.2 // indirect
	github.com/intel/sriov-network-device-plugin v0.0.0-20200924101303-b7f6d3e06797
	github.com/jaypipes/ghw v0.6.1
	github.com/k8snetworkplumbingwg/network-attachment-definition-client v0.0.0-20200626054723-37f83d1996bc
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/openshift/client-go v0.0.0-20200827190008-3062137373b5
	github.com/openshift/machine-config-operator v0.0.1-0.20201023110058-6c8bd9b2915c
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.1.1
	github.com/vishvananda/netlink v1.1.0
	github.com/vishvananda/netns v0.0.0-20191106174202-0a2b9b5464df
	golang.org/x/time v0.0.0-20210220033141-f8bda1e9f3ba
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.20.2
	k8s.io/apiextensions-apiserver v0.20.1
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.20.2
	k8s.io/code-generator v0.20.2
	k8s.io/kubectl v0.20.2
	k8s.io/utils v0.0.0-20210111153108-fddb29f9d009
	sigs.k8s.io/controller-runtime v0.8.3
)
