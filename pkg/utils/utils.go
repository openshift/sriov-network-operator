package utils

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

//go:generate ../../bin/mockgen -destination mock/mock_utils.go -source utils.go
type CmdInterface interface {
	Chroot(string) (func() error, error)
	RunCommand(string, ...string) (string, string, error)
}

type utilsHelper struct {
}

func New() CmdInterface {
	return &utilsHelper{}
}

func (u *utilsHelper) Chroot(path string) (func() error, error) {
	root, err := os.Open("/")
	if err != nil {
		return nil, err
	}

	if err := syscall.Chroot(path); err != nil {
		root.Close()
		return nil, err
	}
	vars.InChroot = true

	return func() error {
		defer root.Close()
		if err := root.Chdir(); err != nil {
			return err
		}
		vars.InChroot = false
		return syscall.Chroot(".")
	}, nil
}

// RunCommand runs a command
func (u *utilsHelper) RunCommand(command string, args ...string) (string, string, error) {
	log.Log.Info("RunCommand()", "command", command, "args", args)
	var stdout, stderr bytes.Buffer

	cmd := exec.Command(command, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	log.Log.V(2).Info("RunCommand()", "output", stdout.String(), "error", err)
	return stdout.String(), stderr.String(), err
}

func IsCommandNotFound(err error) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.ExitStatus() == 127 {
			return true
		}
	}
	return false
}

func GetHostExtension() string {
	if vars.InChroot {
		return vars.FilesystemRoot
	}
	return filepath.Join(vars.FilesystemRoot, consts.Host)
}

func GetHostExtensionPath(path string) string {
	return filepath.Join(GetHostExtension(), path)
}

func GetChrootExtension() string {
	if vars.InChroot {
		return vars.FilesystemRoot
	}
	return fmt.Sprintf("chroot %s%s", vars.FilesystemRoot, consts.Host)
}

// WriteFileWithTimeout writes data to a file with a timeout.
// This is useful for writing to sysfs files where the kernel driver may block
// indefinitely if it is in a bad state.
// Note: if the timeout expires, the write goroutine will remain blocked in the
// kernel; it cannot be canceled but will be cleaned up when the process exits.
func WriteFileWithTimeout(path string, data []byte, perm os.FileMode, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ch := make(chan error, 1)
	go func() {
		ch <- os.WriteFile(path, data, perm)
	}()
	select {
	case err := <-ch:
		return err
	case <-timer.C:
		return fmt.Errorf("timeout writing to file %s after %v", path, timeout)
	}
}
