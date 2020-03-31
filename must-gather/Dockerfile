FROM registry.svc.ci.openshift.org/openshift/release:golang-1.12 AS builder
WORKDIR /go/src/github.com/openshift/sriov-network-operator
COPY . .

FROM quay.io/openshift/origin-must-gather:4.3.0
COPY --from=builder /go/src/github.com/openshift/sriov-network-operator/must-gather/collection-scripts/* /usr/bin/

ENTRYPOINT /usr/bin/gather
