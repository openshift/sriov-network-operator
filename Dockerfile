FROM golang:1.16 AS builder
WORKDIR /go/src/github.com/k8snetworkplumbingwg/sriov-network-operator
COPY . .
RUN make _build-manager BIN_PATH=build/_output/cmd

FROM centos:7
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/sriov-network-operator/build/_output/cmd/manager /usr/bin/sriov-network-operator
COPY bindata /bindata
ENV OPERATOR_NAME=sriov-network-operator
CMD ["/usr/bin/sriov-network-operator"]
