#!/bin/bash
oc apply -f deploy/namespace.yaml
oc apply -f deploy/service_account.yaml
oc apply -f deploy/role.yaml
oc apply -f deploy/role_binding.yaml
oc apply -f deploy/crds/sriovnetwork_v1_sriovnetwork_crd.yaml
oc apply -f deploy/crds/sriovnetwork_v1_sriovnetworknodestate_crd.yaml
oc apply -f deploy/crds/sriovnetwork_v1_sriovnetworknodepolicy_crd.yaml