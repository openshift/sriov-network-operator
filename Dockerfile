FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /go/src/github.com/k8snetworkplumbingwg/sriov-network-operator
COPY . .
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} make _build-manager BIN_PATH=build/_output/cmd && \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} make _build-sriov-network-operator-config-cleanup BIN_PATH=build/_output/cmd

FROM quay.io/centos/centos:stream9
USER 65532:65532
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/sriov-network-operator/build/_output/cmd/manager /usr/bin/sriov-network-operator
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/sriov-network-operator/build/_output/cmd/sriov-network-operator-config-cleanup /usr/bin/sriov-network-operator-config-cleanup
COPY bindata /bindata
ENV OPERATOR_NAME=sriov-network-operator
CMD ["/usr/bin/sriov-network-operator"]
