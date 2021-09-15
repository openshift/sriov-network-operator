package version

import (
	"fmt"
	// "strings"

	"github.com/blang/semver"
)

var (
	// Raw is the string representation of the version. This will be replaced
	// with the calculated version at build time.
	Raw = "v4.10.0"

	// Version is semver representation of the version.
	Version = semver.MustParse("4.10.0")

	// String is the human-friendly representation of the version.
	String = fmt.Sprintf("SriovNetworkConfigOperator %s", Raw)
)
