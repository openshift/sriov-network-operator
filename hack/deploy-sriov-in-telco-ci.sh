#!/usr/bin/env bash
set -xeo pipefail

# load variables
PR=${PR:-""}
BRANCH=${BRANCH:-"master"}
OPERATOR_NAMESPACE="openshift-sriov-network-operator"
BUILD_POD_NAMESPACE="openshift-config"

INTERNAL_REGISTRY="image-registry.openshift-image-registry.svc:5000/openshift-sriov-network-operator"
INDEX_IMAGE=${INTERNAL_REGISTRY}"/sriov-index:latest"
CATALOG_NAME="sriov-index"

# clean up everything
echo "clean repo folder"
rm -rf sriov-network-operator || true

echo "clean index image"
oc -n openshift-marketplace delete catalogsource ${CATALOG_NAME} || true

echo "clean sriov namespace"
oc delete ns $OPERATOR_NAMESPACE --wait=false || true
#sleep 3

echo "remove webhooks"
oc delete mutatingwebhookconfigurations.admissionregistration.k8s.io sriov-operator-webhook-config || true
oc delete mutatingwebhookconfigurations.admissionregistration.k8s.io network-resources-injector-config || true
oc delete validatingwebhookconfigurations.admissionregistration.k8s.io sriov-operator-webhook-config || true

echo "delete sriov crds"
oc get crd | grep sriovnetwork.openshift.io | awk '{print "oc delete crd",$1}' | sh || true

echo "wait for ${OPERATOR_NAMESPACE} namespace to get removed"
ATTEMPTS=0
MAX_ATTEMPTS=72
ready=false
sleep_time=5
until $ready || [ $ATTEMPTS -eq $MAX_ATTEMPTS ]
do
    echo "waiting for ${OPERATOR_NAMESPACE} namespace to be removed"
    if [ `oc get ns | grep ${OPERATOR_NAMESPACE} | wc -l` == 0 ]; then
        echo "${OPERATOR_NAMESPACE} namespace removed}"
        ready=true
    else
        echo "${OPERATOR_NAMESPACE} namespace not removed yet, waiting..."
        sleep $sleep_time
    fi
    ATTEMPTS=$((ATTEMPTS+1))
done

# create sriov namespace
cat <<EOF | oc apply -f -
---
apiVersion: v1
kind: Namespace
metadata:
  name: ${OPERATOR_NAMESPACE}
  annotations:
    workload.openshift.io/allowed: management
---
EOF

oc -n ${BUILD_POD_NAMESPACE} delete cm script || true
oc -n ${BUILD_POD_NAMESPACE} delete po podman || true

SECRET=`oc -n ${BUILD_POD_NAMESPACE} get sa builder -oyaml | grep imagePullSecrets -A 1 | grep -o "builder-.*"`
# allow the build pod to push to the openshift namespace
oc policy add-role-to-user system:image-puller system:serviceaccount:${BUILD_POD_NAMESPACE}:builder --namespace=${OPERATOR_NAMESPACE}
oc policy add-role-to-user system:image-builder system:serviceaccount:${BUILD_POD_NAMESPACE}:builder --namespace=${OPERATOR_NAMESPACE}
oc policy add-role-to-user system:image-puller system:serviceaccount:openshift-marketplace:sriov-index --namespace=${OPERATOR_NAMESPACE}
oc policy add-role-to-user system:image-puller system:serviceaccount:openshift-marketplace:default --namespace=${OPERATOR_NAMESPACE}

# create build script
cat <<'EOF' > ./build-script.sh
#!/usr/bin/env bash
set -xeo pipefail
yum install git jq -y

PULL_SECRET="/var/run/secrets/openshift.io/pull/.dockerconfigjson"

REPO=${REPO:-"https://github.com/openshift/sriov-network-operator.git"}
BRANCH=${BRANCH:-"master"}
PR=${PR:-""}

OPERATOR_NAMESPACE="openshift-sriov-network-operator"
BUILD_POD_NAMESPACE="openshift-config"

CSV_FILE_PATH="manifests/stable/sriov-network-operator.clusterserviceversion.yaml"

INTERNAL_REGISTRY="image-registry.openshift-image-registry.svc:5000/openshift-sriov-network-operator"

# construct image names
INDEX_IMAGE=${INTERNAL_REGISTRY}"/sriov-index:latest"
BUNDLE_IMAGE=${INTERNAL_REGISTRY}"/sriov-bundle:latest"
SRIOV_NETWORK_OPERATOR_IMAGE=${INTERNAL_REGISTRY}"/sriov-network-operator:latest"
SRIOV_NETWORK_CONFIG_DAEMON_IMAGE=${INTERNAL_REGISTRY}"/sriov-network-config-daemon:latest"
SRIOV_NETWORK_WEBHOOK_IMAGE=${INTERNAL_REGISTRY}"/sriov-network-webhook:latest"

# clone the repo
git clone $REPO -b $BRANCH --single-branch

# access repo
cd sriov-network-operator

# if a PR is configured switch to the PR
if [ -n "$PR" ]; then
  echo "cloning sriov repo from PR ${PR}"
  git fetch origin pull/$PR/head:pr
  git checkout pr
fi

mkdir bin || true

EXTERNAL_IMAGES_TAG=`cat manifests/stable/sriov-network-operator.clusterserviceversion.yaml | grep "containerImage: quay.io/openshift/origin-sriov-network-operator" | awk -F':' '{print $NF}'`

EXTERNAL_SRIOV_NETWORK_OPERATOR_IMAGE="quay.io/openshift/origin-sriov-network-operator:${EXTERNAL_IMAGES_TAG}"
EXTERNAL_SRIOV_NETWORK_CONFIG_DAEMON_IMAGE="quay.io/openshift/origin-sriov-network-config-daemon:${EXTERNAL_IMAGES_TAG}"
EXTERNAL_SRIOV_NETWORK_WEBHOOK_IMAGE="quay.io/openshift/origin-sriov-network-webhook:${EXTERNAL_IMAGES_TAG}"

curl -L https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/latest-4.16/opm-linux.tar.gz | tar xvz -C bin

# build containers from the sriov repo
echo "## build operator image"
podman build --authfile=${PULL_SECRET} -t "${SRIOV_NETWORK_OPERATOR_IMAGE}" -f "Dockerfile.rhel7" .

echo "## build daemon image"
podman build --authfile=${PULL_SECRET} -t "${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}" -f "Dockerfile.sriov-network-config-daemon.rhel7" .

echo "## build webhook image"
podman build --authfile=${PULL_SECRET} -t "${SRIOV_NETWORK_WEBHOOK_IMAGE}" -f "Dockerfile.webhook.rhel7" .

set +x
pass=$( jq .\"image-registry.openshift-image-registry.svc:5000\".auth /var/run/secrets/openshift.io/push/.dockercfg )
pass=`echo ${pass:1:-1} | base64 -d`
podman login -u serviceaccount -p ${pass:8} image-registry.openshift-image-registry.svc:5000 --tls-verify=false
set -x

echo "## push operator image"
podman push ${SRIOV_NETWORK_OPERATOR_IMAGE} --tls-verify=false
echo "## push daemon image"
podman push ${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE} --tls-verify=false
echo "## push webhook image"
podman push ${SRIOV_NETWORK_WEBHOOK_IMAGE} --tls-verify=false

# switch images in csv
sed -i "s|${EXTERNAL_SRIOV_NETWORK_OPERATOR_IMAGE}|${SRIOV_NETWORK_OPERATOR_IMAGE}|" ${CSV_FILE_PATH}
sed -i "s|${EXTERNAL_SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}|${SRIOV_NETWORK_CONFIG_DAEMON_IMAGE}|" ${CSV_FILE_PATH}
sed -i "s|${EXTERNAL_SRIOV_NETWORK_WEBHOOK_IMAGE}|${SRIOV_NETWORK_WEBHOOK_IMAGE}|" ${CSV_FILE_PATH}
sed -i "s|IfNotPresent|Always|" ${CSV_FILE_PATH}

# build bundle
echo "## build bundle"
podman build -t "${BUNDLE_IMAGE}" -f "bundleci.Dockerfile" .
echo "## push bundle"
podman push ${BUNDLE_IMAGE} --tls-verify=false

# build index image
echo "## build index image"
bin/opm-rhel8 index add --use-http -p podman --bundles ${BUNDLE_IMAGE} --tag ${INDEX_IMAGE}

echo "## push index image"
podman push ${INDEX_IMAGE} --tls-verify=false
EOF

oc -n ${BUILD_POD_NAMESPACE} create cm script --from-file=./build-script.sh

# start the build pod
cat <<EOF | oc apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: podman
  namespace: openshift-config
spec:
  restartPolicy: Never
  serviceAccountName: builder
  containers:
    - name: priv
      image: quay.io/podman/stable
      env:
      - name: PR
        value: "${PR}"
      - name: BRANCH
        value: "${BRANCH}"
      command:
      - /bin/bash
      - -c
      - cp /var/script/build-script.sh /tmp/ && chmod +x /tmp/build-script.sh && /tmp/build-script.sh
      securityContext:
        privileged: true
      volumeMounts:
        - mountPath: /var/run/secrets/openshift.io/pull
          name: dockercfg
          readOnly: true
        - mountPath: /var/run/secrets/openshift.io/push
          name: push-secret
          readOnly: true
        - mountPath: /var/script
          name: script
          readOnly: true
  terminationGracePeriodSeconds: 5
  serviceAccount: builder
  volumes:
    - name: dockercfg
      defaultMode: 384
      secret:
        secretName: pull-secret
    - name: script
      defaultMode: 777
      configMap:
        name: script
    - name: push-secret
      defaultMode: 384
      secret:
        secretName: ${SECRET}
EOF

# wait for pod to finish
ATTEMPTS=0
MAX_ATTEMPTS=72
ready=false
sleep_time=5
until $ready || [ $ATTEMPTS -eq $MAX_ATTEMPTS ]
do
    if [ `oc -n ${BUILD_POD_NAMESPACE} get po podman | grep Running | wc -l` == 1 ]; then
        ready=true
    else
        sleep $sleep_time
    fi
    ATTEMPTS=$((ATTEMPTS+1))
done

# print pod yaml
oc -n ${BUILD_POD_NAMESPACE} get po podman -oyaml
# run logs
oc -n ${BUILD_POD_NAMESPACE} logs -f podman

# if the pod didn't completed
if [ `oc -n ${BUILD_POD_NAMESPACE} get po podman | grep Completed | wc -l` == 0 ]; then
  echo "build failed"
  exit -1
fi

# create catalogSource
cat <<EOF | oc apply -f -
---
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: ${CATALOG_NAME}
  namespace: openshift-marketplace
spec:
  displayName: CI Index
  image: ${INDEX_IMAGE}
  publisher: Red Hat
  sourceType: grpc
  updateStrategy:
    registryPoll:
      interval: 10m0s
---
EOF


# create operator group
cat <<EOF | oc apply -f -
---
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: sriov-network-operators
  namespace: openshift-sriov-network-operator
spec:
  targetNamespaces:
  - openshift-sriov-network-operator
---
EOF

# create subscription
cat <<EOF | oc apply -f -
---
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: sriov-network-operator-subscription
  namespace: openshift-sriov-network-operator
spec:
  channel: "alpha"
  name: sriov-network-operator
  source: ${CATALOG_NAME}
  sourceNamespace: openshift-marketplace
---
EOF

# wait for the operator to get installed
echo "wait for csv to be installed"
ATTEMPTS=0
MAX_ATTEMPTS=72
ready=false
sleep_time=5
until $ready || [ $ATTEMPTS -eq $MAX_ATTEMPTS ]
do
    echo "waiting csv to be installed"
    if [ `oc -n ${OPERATOR_NAMESPACE} get csv | grep sriov-network-operator | grep Succeeded | wc -l` == 1 ]; then
        echo "csv installed}"
        ready=true
    else
        echo "csv not installed yet, waiting..."
        sleep $sleep_time
    fi
    ATTEMPTS=$((ATTEMPTS+1))
done

# apply sriovOperatorConfig
cat <<EOF | oc apply -f -
---
apiVersion: sriovnetwork.openshift.io/v1
kind: SriovOperatorConfig
metadata:
  name: default
  namespace: openshift-sriov-network-operator
spec:
  enableInjector: true
  enableOperatorWebhook: true
  logLevel: 2
  disableDrain: false
---
EOF

echo "Deployment done"