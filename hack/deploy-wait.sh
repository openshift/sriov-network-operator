#!/bin/bash

ATTEMPTS=0
MAX_ATTEMPTS=72
ready=false
sleep_time=10

until $ready || [ $ATTEMPTS -eq $MAX_ATTEMPTS ]
do
    echo "running tests"
    if SUITE=./test/validation ./hack/run-e2e-conformance.sh; then
        echo "succeeded"
        ready=true
    else    
        echo "failed, retrying"
        sleep $sleep_time
    fi
    (( ATTEMPTS++ ))

    if [[ $ATTEMPTS -eq 25 ]]; then 
       oc delete pods -n openshift-sriov-network-operator --all
    fi
done

if ! $ready; then 
    echo "Timed out waiting for features to be ready"
    ${OPERATOR_EXEC} get nodes
    ${OPERATOR_EXEC} cluster-info dump -n ${NAMESPACE}
    exit 1
fi
