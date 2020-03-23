package e2e

import (
	"fmt"
	"testing"

	f "github.com/operator-framework/operator-sdk/pkg/test"
)

func TestMain(m *testing.M) {
	fmt.Printf("Start E2E TestMain\n")
	f.MainEntry(m)
}
