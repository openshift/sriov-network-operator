module github.com/openshift/sriov-network-operator

go 1.13

require (
	cloud.google.com/go v0.53.0 // indirect
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/Masterminds/sprig v2.22.0+incompatible
	github.com/blang/semver v3.5.0+incompatible
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/fsnotify/fsnotify v1.4.9
	github.com/go-logr/logr v0.1.0
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/huandu/xstrings v1.3.2 // indirect
	github.com/intel/sriov-network-device-plugin v0.0.0-20200603101849-e116e9c7d0b8

	github.com/jaypipes/ghw v0.6.1
	github.com/k8snetworkplumbingwg/network-attachment-definition-client v0.0.0-20200626054723-37f83d1996bc
	github.com/mitchellh/copystructure v1.0.0 // indirect
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	github.com/openshift/client-go v0.0.0-20200320150128-a906f3d8e723
	github.com/openshift/machine-config-operator v4.2.0-alpha.0.0.20190917115525-033375cbe820+incompatible

	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.5.1 // indirect
	github.com/spf13/cobra v0.0.5
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/net v0.0.0-20200625001655-4c5254603344 // indirect
	golang.org/x/sys v0.0.0-20200625212154-ddb9806d33ae // indirect
	golang.org/x/time v0.0.0-20190921001708-c4c64cad1fd0
	google.golang.org/protobuf v1.25.0 // indirect
	k8s.io/api v0.18.8
	k8s.io/apiextensions-apiserver v0.18.8 // indirect
	k8s.io/apimachinery v0.18.8
	k8s.io/client-go v0.18.8
	k8s.io/kubectl v0.0.0
	k8s.io/utils v0.0.0-20200603063816-c1c6865ac451
	sigs.k8s.io/controller-runtime v0.6.2
)

replace (
	github.com/openshift/machine-config-operator => github.com/openshift/machine-config-operator v0.0.1-0.20200606015452-b51bca192089 // release-4.6
	k8s.io/kubectl => k8s.io/kubectl v0.18.8
	k8s.io/kubelet => k8s.io/kubelet v0.18.8
)
