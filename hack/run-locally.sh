#!/bin/bash
env $(cat hack/env.sh) operator-sdk up local --namespace sriov-network-operator
