#!/bin/bash
EXCLUSIONS=(operator.yaml) hack/deploy-setup.sh sriov-network-operator
source hack/env.sh
operator-sdk up local --namespace sriov-network-operator
