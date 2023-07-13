/*
Copyright 2023.

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
package host

import (
	"fmt"
	"os"
	pathlib "path"
	"strings"

	"github.com/golang/glog"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

const (
	hostPathFromDaemon    = "/host"
	redhatReleaseFile     = "/etc/redhat-release"
	rhelRDMAConditionFile = "/usr/libexec/rdma-init-kernel"
	rhelRDMAServiceName   = "rdma"
	rhelPackageManager    = "yum"

	ubuntuRDMAConditionFile = "/usr/sbin/rdma-ndd"
	ubuntuRDMAServiceName   = "rdma-ndd"
	ubuntuPackageManager    = "apt-get"

	genericOSReleaseFile = "/etc/os-release"
)

// Contains all the host manipulation functions
//
//go:generate ../../bin/mockgen -destination mock/mock_host.go -source host.go
type HostManagerInterface interface {
	TryEnableTun()
	TryEnableVhostNet()
	TryEnableRdma() (bool, error)

	// private functions
	// part of the interface for the mock generation
	LoadKernelModule(name string, args ...string) error
	isKernelModuleLoaded(string) (bool, error)
	isRHELSystem() (bool, error)
	isUbuntuSystem() (bool, error)
	isCoreOS() (bool, error)
	rdmaIsLoaded() (bool, error)
	enableRDMA(string, string, string) (bool, error)
	installRDMA(string) error
	triggerUdevEvent() error
	reloadDriver(string) error
	enableRDMAOnRHELMachine() (bool, error)
	getOSPrettyName() (string, error)
}

type HostManager struct {
	RunOnHost bool
	cmd       utils.CommandInterface
}

func NewHostManager(runOnHost bool) HostManagerInterface {
	return &HostManager{
		RunOnHost: runOnHost,
		cmd:       &utils.Command{},
	}
}

func (h *HostManager) LoadKernelModule(name string, args ...string) error {
	glog.Infof("LoadKernelModule(): try to load kernel module %s with arguments '%s'", name, args)
	chrootDefinition := getChrootExtention(h.RunOnHost)
	cmdArgs := strings.Join(args, " ")

	// check if the driver is already loaded in to the system
	isLoaded, err := h.isKernelModuleLoaded(name)
	if err != nil {
		glog.Errorf("LoadKernelModule(): failed to check if kernel module %s is already loaded", name)
	}
	if isLoaded {
		glog.Infof("LoadKernelModule(): kernel module %s already loaded", name)
		return nil
	}

	_, _, err = h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("%s modprobe %s %s", chrootDefinition, name, cmdArgs))
	if err != nil {
		glog.Errorf("LoadKernelModule(): failed to load kernel module %s with arguments '%s': %v", name, args, err)
		return err
	}
	return nil
}

func (h *HostManager) isKernelModuleLoaded(kernelModuleName string) (bool, error) {
	glog.Infof("isKernelModuleLoaded(): check if kernel module %s is loaded", kernelModuleName)
	chrootDefinition := getChrootExtention(h.RunOnHost)

	stdout, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("%s lsmod | grep \"^%s\"", chrootDefinition, kernelModuleName))
	if err != nil && stderr.Len() != 0 {
		glog.Errorf("isKernelModuleLoaded(): failed to check if kernel module %s is loaded: error: %v stderr %s", kernelModuleName, err, stderr.String())
		return false, err
	}
	glog.V(2).Infof("isKernelModuleLoaded(): %v", stdout.String())
	if stderr.Len() != 0 {
		glog.Errorf("isKernelModuleLoaded(): failed to check if kernel module %s is loaded: error: %v stderr %s", kernelModuleName, err, stderr.String())
		return false, fmt.Errorf(stderr.String())
	}

	if stdout.Len() != 0 {
		glog.Infof("isKernelModuleLoaded(): kernel module %s already loaded", kernelModuleName)
		return true, nil
	}

	return false, nil
}

func (h *HostManager) TryEnableTun() {
	if err := h.LoadKernelModule("tun"); err != nil {
		glog.Errorf("tryEnableTun(): TUN kernel module not loaded: %v", err)
	}
}

func (h *HostManager) TryEnableVhostNet() {
	if err := h.LoadKernelModule("vhost_net"); err != nil {
		glog.Errorf("tryEnableVhostNet(): VHOST_NET kernel module not loaded: %v", err)
	}
}

func (h *HostManager) TryEnableRdma() (bool, error) {
	glog.V(2).Infof("tryEnableRdma()")
	chrootDefinition := getChrootExtention(h.RunOnHost)

	// check if the driver is already loaded in to the system
	_, stderr, mlx4Err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("grep --quiet 'mlx4_en' <(%s lsmod)", chrootDefinition))
	if mlx4Err != nil && stderr.Len() != 0 {
		glog.Errorf("tryEnableRdma(): failed to check for kernel module 'mlx4_en': error: %v stderr %s", mlx4Err, stderr.String())
		return false, fmt.Errorf(stderr.String())
	}

	_, stderr, mlx5Err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("grep --quiet 'mlx5_core' <(%s lsmod)", chrootDefinition))
	if mlx5Err != nil && stderr.Len() != 0 {
		glog.Errorf("tryEnableRdma(): failed to check for kernel module 'mlx5_core': error: %v stderr %s", mlx5Err, stderr.String())
		return false, fmt.Errorf(stderr.String())
	}

	if mlx4Err != nil && mlx5Err != nil {
		glog.Errorf("tryEnableRdma(): no RDMA capable devices")
		return false, nil
	}

	isRhelSystem, err := h.isRHELSystem()
	if err != nil {
		glog.Errorf("tryEnableRdma(): failed to check if the machine is base on RHEL: %v", err)
		return false, err
	}

	// RHEL check
	if isRhelSystem {
		return h.enableRDMAOnRHELMachine()
	}

	isUbuntuSystem, err := h.isUbuntuSystem()
	if err != nil {
		glog.Errorf("tryEnableRdma(): failed to check if the machine is base on Ubuntu: %v", err)
		return false, err
	}

	if isUbuntuSystem {
		return h.enableRDMAOnUbuntuMachine()
	}

	osName, err := h.getOSPrettyName()
	if err != nil {
		glog.Errorf("tryEnableRdma(): failed to check OS name: %v", err)
		return false, err
	}

	glog.Errorf("tryEnableRdma(): Unsupported OS: %s", osName)
	return false, fmt.Errorf("unable to load RDMA unsupported OS: %s", osName)
}

func (h *HostManager) enableRDMAOnRHELMachine() (bool, error) {
	glog.Infof("enableRDMAOnRHELMachine()")
	isCoreOsSystem, err := h.isCoreOS()
	if err != nil {
		glog.Errorf("enableRDMAOnRHELMachine(): failed to check if the machine runs CoreOS: %v", err)
		return false, err
	}

	// CoreOS check
	if isCoreOsSystem {
		isRDMALoaded, err := h.rdmaIsLoaded()
		if err != nil {
			glog.Errorf("enableRDMAOnRHELMachine(): failed to check if RDMA kernel modules are loaded: %v", err)
			return false, err
		}

		return isRDMALoaded, nil
	}

	// RHEL
	glog.Infof("enableRDMAOnRHELMachine(): enabling RDMA on RHEL machine")
	isRDMAEnable, err := h.enableRDMA(rhelRDMAConditionFile, rhelRDMAServiceName, rhelPackageManager)
	if err != nil {
		glog.Errorf("enableRDMAOnRHELMachine(): failed to enable RDMA on RHEL machine: %v", err)
		return false, err
	}

	// check if we need to install rdma-core package
	if isRDMAEnable {
		isRDMALoaded, err := h.rdmaIsLoaded()
		if err != nil {
			glog.Errorf("enableRDMAOnRHELMachine(): failed to check if RDMA kernel modules are loaded: %v", err)
			return false, err
		}

		// if ib kernel module is not loaded trigger a loading
		if isRDMALoaded {
			err = h.triggerUdevEvent()
			if err != nil {
				glog.Errorf("enableRDMAOnRHELMachine() failed to trigger udev event: %v", err)
				return false, err
			}
		}
	}

	return true, nil
}

func (h *HostManager) enableRDMAOnUbuntuMachine() (bool, error) {
	glog.Infof("enableRDMAOnUbuntuMachine(): enabling RDMA on RHEL machine")
	isRDMAEnable, err := h.enableRDMA(ubuntuRDMAConditionFile, ubuntuRDMAServiceName, ubuntuPackageManager)
	if err != nil {
		glog.Errorf("enableRDMAOnUbuntuMachine(): failed to enable RDMA on Ubuntu machine: %v", err)
		return false, err
	}

	// check if we need to install rdma-core package
	if isRDMAEnable {
		isRDMALoaded, err := h.rdmaIsLoaded()
		if err != nil {
			glog.Errorf("enableRDMAOnUbuntuMachine(): failed to check if RDMA kernel modules are loaded: %v", err)
			return false, err
		}

		// if ib kernel module is not loaded trigger a loading
		if isRDMALoaded {
			err = h.triggerUdevEvent()
			if err != nil {
				glog.Errorf("enableRDMAOnUbuntuMachine() failed to trigger udev event: %v", err)
				return false, err
			}
		}
	}

	return true, nil
}

func (h *HostManager) isRHELSystem() (bool, error) {
	glog.Infof("isRHELSystem(): checking for RHEL machine")
	path := redhatReleaseFile
	if !h.RunOnHost {
		path = pathlib.Join(hostPathFromDaemon, path)
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			glog.V(2).Infof("isRHELSystem() not a RHEL machine")
			return false, nil
		}

		glog.Errorf("isRHELSystem() failed to check for os release file on path %s: %v", path, err)
		return false, err
	}

	return true, nil
}

func (h *HostManager) isCoreOS() (bool, error) {
	glog.Infof("isCoreOS(): checking for CoreOS machine")
	path := redhatReleaseFile
	if !h.RunOnHost {
		path = pathlib.Join(hostPathFromDaemon, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		glog.Errorf("isCoreOS(): failed to read RHEL release file on path %s: %v", path, err)
		return false, err
	}

	if strings.Contains(string(data), "CoreOS") {
		return true, nil
	}

	return false, nil
}

func (h *HostManager) isUbuntuSystem() (bool, error) {
	glog.Infof("isUbuntuSystem(): checking for Ubuntu machine")
	path := genericOSReleaseFile
	if !h.RunOnHost {
		path = pathlib.Join(hostPathFromDaemon, path)
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			glog.Errorf("isUbuntuSystem() os-release on path %s doesn't exist: %v", path, err)
			return false, err
		}

		glog.Errorf("isUbuntuSystem() failed to check for os release file on path %s: %v", path, err)
		return false, err
	}

	stdout, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("grep -i --quiet 'ubuntu' %s", path))
	if err != nil && stderr.Len() != 0 {
		glog.Errorf("isUbuntuSystem(): failed to check for ubuntu operating system name in os-releasae file: error: %v stderr %s", err, stderr.String())
		return false, fmt.Errorf(stderr.String())
	}

	if stdout.Len() > 0 {
		return true, nil
	}

	return false, nil
}

func (h *HostManager) rdmaIsLoaded() (bool, error) {
	glog.V(2).Infof("rdmaIsLoaded()")
	chrootDefinition := getChrootExtention(h.RunOnHost)

	// check if the driver is already loaded in to the system
	_, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("grep --quiet '\\(^ib\\|^rdma\\)' <(%s lsmod)", chrootDefinition))
	if err != nil && stderr.Len() != 0 {
		glog.Errorf("rdmaIsLoaded(): fail to check if ib and rdma kernel modules are loaded: error: %v stderr %s", err, stderr.String())
		return false, fmt.Errorf(stderr.String())
	}

	if err != nil {
		return false, nil
	}

	return true, nil
}

func (h *HostManager) enableRDMA(conditionFilePath, serviceName, packageManager string) (bool, error) {
	path := conditionFilePath
	if !h.RunOnHost {
		path = pathlib.Join(hostPathFromDaemon, path)
	}
	glog.Infof("enableRDMA(): checking for service file on path %s", path)

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			glog.V(2).Infof("enableRDMA(): RDMA server doesn't exist")
			err = h.installRDMA(packageManager)
			if err != nil {
				glog.Errorf("enableRDMA() failed to install RDMA package: %v", err)
				return false, err
			}

			err = h.triggerUdevEvent()
			if err != nil {
				glog.Errorf("enableRDMA() failed to trigger udev event: %v", err)
				return false, err
			}

			return false, nil
		}

		glog.Errorf("enableRDMA() failed to check for os release file on path %s: %v", path, err)
		return false, err
	}

	glog.Infof("enableRDMA(): service %s.service installed", serviceName)
	return true, nil
}

func (h *HostManager) installRDMA(packageManager string) error {
	glog.Infof("installRDMA(): installing RDMA")
	chrootDefinition := getChrootExtention(h.RunOnHost)

	stdout, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("%s %s install -y rdma-core", chrootDefinition, packageManager))
	if err != nil && stderr.Len() != 0 {
		glog.Errorf("installRDMA(): failed to install RDMA package output %s: error %v stderr %s", stdout.String(), err, stderr.String())
		return err
	}

	return nil
}

func (h *HostManager) triggerUdevEvent() error {
	glog.Infof("triggerUdevEvent(): installing RDMA")

	err := h.reloadDriver("mlx4_en")
	if err != nil {
		return err
	}

	err = h.reloadDriver("mlx5_core")
	if err != nil {
		return err
	}

	return nil
}

func (h *HostManager) reloadDriver(driverName string) error {
	glog.Infof("reloadDriver(): reload driver %s", driverName)
	chrootDefinition := getChrootExtention(h.RunOnHost)

	_, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("%s modprobe -r %s && %s modprobe %s", chrootDefinition, driverName, chrootDefinition, driverName))
	if err != nil && stderr.Len() != 0 {
		glog.Errorf("installRDMA(): failed to reload %s kernel module: error %v stderr %s", driverName, err, stderr.String())
		return err
	}

	return nil
}

func (h *HostManager) getOSPrettyName() (string, error) {
	path := genericOSReleaseFile
	if !h.RunOnHost {
		path = pathlib.Join(hostPathFromDaemon, path)
	}

	glog.Infof("getOSPrettyName(): getting os name from os-release file")

	stdout, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("cat %s | grep PRETTY_NAME | cut -c 13-", path))
	if err != nil && stderr.Len() != 0 {
		glog.Errorf("isUbuntuSystem(): failed to check for ubuntu operating system name in os-releasae file: error: %v stderr %s", err, stderr.String())
		return "", fmt.Errorf(stderr.String())
	}

	if stdout.Len() > 0 {
		return stdout.String(), nil
	}

	return "", fmt.Errorf("failed to find pretty operating system name")
}

func getChrootExtention(runOnHost bool) string {
	if !runOnHost {
		return "chroot /host/"
	}
	return ""
}
