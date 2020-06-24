package connectivity

import "fmt"

// ConnectivityTest .
type ConnectivityTest struct {
	Parameter []Parameters
}

//An Parameters represents individual parameter for connectivity tests
type Parameters struct {
	Name  string
	Value string
}

func (c ConnectivityTest) mtuParameterValidation(MTU string) bool {
	fmt.Println(MTU)
	fmt.Println("Validate if policy supports this specific MTU")
	return true
}

func (c ConnectivityTest) protocolParameterValidation(Protocol string) bool {
	fmt.Println(Protocol)
	fmt.Println("Validate if policy supports this specific Protocol")
	return true
}

func (c ConnectivityTest) connectivityParameterValidation(Connectivity string) bool {
	fmt.Println(Connectivity)
	fmt.Println("Validate if policy supports this specific Connectivity type")
	return true
}

// RunTest starts connecitivity tests based on given parameters.
func (c ConnectivityTest) RunTest() bool {
	fmt.Println("Run Tests")
	return true
}

// EnvValidation validates if it's possible to run specfic connectivity test on given environment.
func (c ConnectivityTest) EnvValidation() bool {
	for _, value := range c.Parameter {
		if value.Name == "Protocol" {
			c.protocolParameterValidation(value.Value)
		} else if value.Name == "Connectivity" {
			c.connectivityParameterValidation(value.Value)
		} else if value.Name == "MTU" {
			c.mtuParameterValidation(value.Value)
		}
	}
	return true
}

// NewConnectivityTest Get new instance of ConnectivityTest
func NewConnectivityTest() ConnectivityTest {
	return ConnectivityTest{}
}
