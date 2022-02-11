# Hacking Guide

The sriov network operator relies on operator-sdk and kubebuilder to scaffold and generate code and manifests. We keeps upgrading sdk version for the operator. Now the operator is compliance with operator-sdk 1.9.0 and go.kubebuilder.io/v3.

## Build and Test

To run the operator locally, make sure the env variable `KUBECONFIG` is set properly.

````bash
make run
````

To run the e2e test.

```bash
make test-e2e
```

To build the binary.

```bash
# build all components
make all

# build the manager
make manager

# build the plugins
make plugins
```

If you want to test changes to the `network config daemon`, you must:
- build and tag an image locally with `docker build -f Dockerfile.sriov-network-config-daemon -t imagename`
- push the image to a registry
- change `hack/env.sh` value for `SRIOV_NETWORK_CONFIG_DAEMON_IMAGE` pointing _imagename_ from the registry you pushed the image to

and then `make run`

## Adding new APIs

Refer to the operator-sdk's [instruction](https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/#create-a-new-api-and-controller).

## Updating existing APIs

1. Edit the *_types.go file to change the definitions for the Spec and Status of the Kinds.

2. Generate Go code, CRD.
    ```bash
    # Generate controller Go code
    make generate
    # Generate CRD
    make manifests
    # Generate go-client code (optional)
    make update-codegen
    ```

3. Add feature logic code to the operator reconciliation loop or the config daemon.

4. Create tests.

## Upgrading operator-sdk

To upgrade the generated code to a new operator-sdk version, we need to follow the instructions in [operator-sdk's migration guide](https://sdk.operatorframework.io/docs/upgrading-sdk-version/).

In addition, we must ensure that the k8s dependencies in the operator's go.mod match the selected version of operator-sdk. For example, for operator-sdk v0.19.x, check the k8s dependencies:

Identify kubebuilder version referenced by operator-sdk

Identify controller-runtime version referenced by kubebuilder

Check controller-runtime's go.mod file

As a result, we can determine the versions of the k8s dependencies in the operator's go.mod.

## Build an custom image

To build the SR-IOV network operator container image:

    ```bash
    make image

If you want to build another image (e.g. webhook or config-daemon), you'll need to do
the following:

    ```bash
    export DOCKERFILE=Dockerfile.sriov-network-config-daemon
    export APP_NAME=sriov-network-config-daemon
    make image

    export DOCKERFILE=Dockerfile.webhook
    export APP_NAME=sriov-network-webhook
    make image

Then you'll need to push the image to a registry using e.g. `buildah push`.
Before deploying the Operator, you want to export these variables to use that custom image:

    ```bash
    export SRIOV_NETWORK_CONFIG_DAEMON_IMAGE=<path to custom image>
    (...)
