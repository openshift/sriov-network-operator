## Jenkins CI examples
This folder holds examples for jenkins CI.

### sriov-network-operator-ci.yaml
This file holds an example jenkins-job-builder configuration that would be triggered on PRs by the admin list, and would simply run the `hack/run-e2e-test-kind.sh` script with `system-service` netns device switcher.
