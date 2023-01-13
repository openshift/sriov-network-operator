#!/bin/bash
set -x
which ginkgo
if [ $? -ne 0 ]; then
# we are moving to a temp folder as in go.mod we have a dependency that is not
# resolved if we are not using google's GOPROXY. That is not the case when building as
# we are using vendored dependencies
	GINKGO_TMP_DIR=$(mktemp -d)
	cd $GINKGO_TMP_DIR
	go mod init tmp
	go install -mod=readonly github.com/onsi/ginkgo/v2/ginkgo@v2.5.0
	rm -rf $GINKGO_TMP_DIR	
	echo "Downloading ginkgo tool"
	cd -
fi

GOPATH="${GOPATH:-~/go}"
JUNIT_OUTPUT="${JUNIT_OUTPUT:-/tmp/artifacts}"
export PATH=$PATH:$GOPATH/bin

GOFLAGS=-mod=vendor ginkgo -output-dir=$JUNIT_OUTPUT --junit-report "unit_report.xml" "$SUITE"
