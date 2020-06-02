package operator

import (
	"fmt"
	"testing"

	f "github.com/operator-framework/operator-sdk/pkg/test"
)

func TestMain(m *testing.M) {
	fmt.Printf("Start Operator TestMain\n")
	f.MainEntry(m)
}
