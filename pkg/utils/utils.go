package utils

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

const (
	httpRequestTimeout = 5 * time.Second
)

//go:generate ../../bin/mockgen -destination mock/mock_utils.go -source utils.go
type CmdInterface interface {
	Chroot(string) (func() error, error)
	RunCommand(string, ...string) (string, string, error)
	HTTPGetFetchData(string) (string, error)
}

type utilsHelper struct {
}

func New() CmdInterface {
	return &utilsHelper{}
}

// Chroot run a chroot command on a specific path
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

func (u *utilsHelper) HTTPGetFetchData(url string) (string, error) {
	// Initialize an HTTP client with a specific timeout.
	client := http.Client{
		Timeout: httpRequestTimeout,
	}

	// Perform the GET request.
	resp, err := client.Get(url)
	if err != nil {
		// This error typically indicates a network issue or that the server is unreachable.
		return "", fmt.Errorf("HTTP GET request to %s failed: %w", url, err)
	}
	// Ensure the response body is closed after the function returns.
	defer resp.Body.Close()

	// Check if the HTTP status code is OK (200).
	if resp.StatusCode != http.StatusOK {
		// Attempt to read the body for more detailed error information if available.
		errorBodyBytes, _ := ioutil.ReadAll(resp.Body) // ReadAll might return its own error, but we prioritize the status code error.
		return "", fmt.Errorf("request to %s returned status %s: %s", url, resp.Status, string(errorBodyBytes))
	}

	// Read the entire response body.
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body from %s: %w", url, err)
	}
	return string(bodyBytes), nil
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
