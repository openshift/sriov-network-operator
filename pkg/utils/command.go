/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package utils

import (
	"bytes"
	"os/exec"
)

// Interface to run commands
//
//go:generate ../../bin/mockgen -destination mock/mock_command.go -source command.go
type CommandInterface interface {
	Run(string, ...string) (stdout bytes.Buffer, stderr bytes.Buffer, err error)
}

type Command struct {
}

func (c *Command) Run(name string, args ...string) (stdout bytes.Buffer, stderr bytes.Buffer, err error) {
	var stdoutbuff, stderrbuff bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &stdoutbuff
	cmd.Stderr = &stderrbuff

	err = cmd.Run()
	return
}
