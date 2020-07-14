package plugins

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/golang/glog"
)

const scriptsPath = "bindata/scripts/enable-kargs.sh"

func TryEnableIommuInKernelArgs() (bool, error) {
	glog.Info("generic-plugin tryEnableIommuInKernelArgs()")
	args := [2]string{"intel_iommu=on", "iommu=pt"}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("/bin/sh", scriptsPath, args[0], args[1])
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// if grubby is not there log and assume kernel args are set correctly.
		if isCommandNotFound(err) {
			glog.Error("generic-plugin tryEnableIommuInKernelArgs(): grubby command not found. Please ensure that kernel args intel_iommu=on iommu=pt are set")
			return false, nil
		}
		glog.Errorf("generic-plugin tryEnableIommuInKernelArgs(): fail to enable iommu %s: %v", args, err)
		return false, err
	}

	i, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err == nil {
		if i > 0 {
			glog.Infof("generic-plugin tryEnableIommuInKernelArgs(): need to reboot node")
			return true, nil
		}
	}
	return false, err
}

func isCommandNotFound(err error) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.ExitStatus() == 127 {
			return true
		}
	}
	return false
}
