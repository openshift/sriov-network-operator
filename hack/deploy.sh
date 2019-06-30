#!/bin/bash

source "$(dirname $0)/common"

${OPERATOR_EXEC} apply -f deploy/namespace.yaml
${OPERATOR_EXEC} apply -f deploy/service_account.yaml
${OPERATOR_EXEC} apply -f deploy/role.yaml
${OPERATOR_EXEC} apply -f deploy/role_binding.yaml
${OPERATOR_EXEC} apply -f deploy/crds/sriovnetwork_v1_sriovnetwork_crd.yaml
${OPERATOR_EXEC} apply -f deploy/crds/sriovnetwork_v1_sriovnetworknodestate_crd.yaml
${OPERATOR_EXEC} apply -f deploy/crds/sriovnetwork_v1_sriovnetworknodepolicy_crd.yaml
