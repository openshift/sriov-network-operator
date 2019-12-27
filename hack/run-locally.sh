#!/bin/bash
EXCLUSIONS=(operator.yaml) hack/deploy-setup.sh ${NAMESPACE}
source hack/env.sh
# operator-sdk up local --namespace ${NAMESPACE}
operator-sdk up local --namespace ${NAMESPACE}
