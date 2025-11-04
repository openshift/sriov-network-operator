FROM golang:1.25 AS builder
WORKDIR /go/src/github.com/k8snetworkplumbingwg/sriov-network-operator
COPY . .
RUN make _build-manager BIN_PATH=build/_output/cmd
RUN make _build-sriov-network-operator-config-cleanup BIN_PATH=build/_output/cmd

FROM quay.io/centos/centos:stream9
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/sriov-network-operator/build/_output/cmd/manager /usr/bin/sriov-network-operator
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/sriov-network-operator/build/_output/cmd/sriov-network-operator-config-cleanup /usr/bin/sriov-network-operator-config-cleanup
COPY bindata /bindata
ENV OPERATOR_NAME=sriov-network-operator
CMD ["/usr/bin/sriov-network-operator"]
