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

	"sigs.k8s.io/controller-runtime/pkg/log"

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
	IsKernelModuleLoaded(string) (bool, error)
	IsRHELSystem() (bool, error)
	IsUbuntuSystem() (bool, error)
	IsCoreOS() (bool, error)
	RdmaIsLoaded() (bool, error)
	EnableRDMA(string, string, string) (bool, error)
	InstallRDMA(string) error
	TriggerUdevEvent() error
	ReloadDriver(string) error
	EnableRDMAOnRHELMachine() (bool, error)
	GetOSPrettyName() (string, error)
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
	log.Log.Info("LoadKernelModule(): try to load kernel module", "name", name, "args", args)
	chrootDefinition := getChrootExtension(h.RunOnHost)
	cmdArgs := strings.Join(args, " ")

	// check if the driver is already loaded in to the system
	isLoaded, err := h.IsKernelModuleLoaded(name)
	if err != nil {
		log.Log.Error(err, "LoadKernelModule(): failed to check if kernel module is already loaded", "name", name)
	}
	if isLoaded {
		log.Log.Info("LoadKernelModule(): kernel module already loaded", "name", name)
		return nil
	}

	_, _, err = h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("%s modprobe %s %s", chrootDefinition, name, cmdArgs))
	if err != nil {
		log.Log.Error(err, "LoadKernelModule(): failed to load kernel module with arguments", "name", name, "args", args)
		return err
	}
	return nil
}

func (h *HostManager) IsKernelModuleLoaded(kernelModuleName string) (bool, error) {
	log.Log.Info("IsKernelModuleLoaded(): check if kernel module is loaded", "name", kernelModuleName)
	chrootDefinition := getChrootExtension(h.RunOnHost)

	stdout, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("%s lsmod | grep \"^%s\"", chrootDefinition, kernelModuleName))
	if err != nil && stderr.Len() != 0 {
		log.Log.Error(err, "IsKernelModuleLoaded(): failed to check if kernel module is loaded",
			"name", kernelModuleName, "stderr", stderr.String())
		return false, err
	}
	log.Log.V(2).Info("IsKernelModuleLoaded():", "stdout", stdout.String())
	if stderr.Len() != 0 {
		log.Log.Error(err, "IsKernelModuleLoaded(): failed to check if kernel module is loaded", "name", kernelModuleName, "stderr", stderr.String())
		return false, fmt.Errorf(stderr.String())
	}

	if stdout.Len() != 0 {
		log.Log.Info("IsKernelModuleLoaded(): kernel module already loaded", "name", kernelModuleName)
		return true, nil
	}

	return false, nil
}

func (h *HostManager) TryEnableTun() {
	if err := h.LoadKernelModule("tun"); err != nil {
		log.Log.Error(err, "tryEnableTun(): TUN kernel module not loaded")
	}
}

func (h *HostManager) TryEnableVhostNet() {
	if err := h.LoadKernelModule("vhost_net"); err != nil {
		log.Log.Error(err, "tryEnableVhostNet(): VHOST_NET kernel module not loaded")
	}
}

func (h *HostManager) TryEnableRdma() (bool, error) {
	log.Log.V(2).Info("tryEnableRdma()")
	chrootDefinition := getChrootExtension(h.RunOnHost)

	// check if the driver is already loaded in to the system
	_, stderr, mlx4Err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("grep --quiet 'mlx4_en' <(%s lsmod)", chrootDefinition))
	if mlx4Err != nil && stderr.Len() != 0 {
		log.Log.Error(mlx4Err, "tryEnableRdma(): failed to check for kernel module 'mlx4_en'", "stderr", stderr.String())
		return false, fmt.Errorf(stderr.String())
	}

	_, stderr, mlx5Err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("grep --quiet 'mlx5_core' <(%s lsmod)", chrootDefinition))
	if mlx5Err != nil && stderr.Len() != 0 {
		log.Log.Error(mlx5Err, "tryEnableRdma(): failed to check for kernel module 'mlx5_core'", "stderr", stderr.String())
		return false, fmt.Errorf(stderr.String())
	}

	if mlx4Err != nil && mlx5Err != nil {
		log.Log.Error(nil, "tryEnableRdma(): no RDMA capable devices")
		return false, nil
	}

	isRhelSystem, err := h.IsRHELSystem()
	if err != nil {
		log.Log.Error(err, "tryEnableRdma(): failed to check if the machine is base on RHEL")
		return false, err
	}

	// RHEL check
	if isRhelSystem {
		return h.EnableRDMAOnRHELMachine()
	}

	isUbuntuSystem, err := h.IsUbuntuSystem()
	if err != nil {
		log.Log.Error(err, "tryEnableRdma(): failed to check if the machine is base on Ubuntu")
		return false, err
	}

	if isUbuntuSystem {
		return h.EnableRDMAOnUbuntuMachine()
	}

	osName, err := h.GetOSPrettyName()
	if err != nil {
		log.Log.Error(err, "tryEnableRdma(): failed to check OS name")
		return false, err
	}

	log.Log.Error(nil, "tryEnableRdma(): Unsupported OS", "name", osName)
	return false, fmt.Errorf("unable to load RDMA unsupported OS: %s", osName)
}

func (h *HostManager) EnableRDMAOnRHELMachine() (bool, error) {
	log.Log.Info("EnableRDMAOnRHELMachine()")
	isCoreOsSystem, err := h.IsCoreOS()
	if err != nil {
		log.Log.Error(err, "EnableRDMAOnRHELMachine(): failed to check if the machine runs CoreOS")
		return false, err
	}

	// CoreOS check
	if isCoreOsSystem {
		isRDMALoaded, err := h.RdmaIsLoaded()
		if err != nil {
			log.Log.Error(err, "EnableRDMAOnRHELMachine(): failed to check if RDMA kernel modules are loaded")
			return false, err
		}

		return isRDMALoaded, nil
	}

	// RHEL
	log.Log.Info("EnableRDMAOnRHELMachine(): enabling RDMA on RHEL machine")
	isRDMAEnable, err := h.EnableRDMA(rhelRDMAConditionFile, rhelRDMAServiceName, rhelPackageManager)
	if err != nil {
		log.Log.Error(err, "EnableRDMAOnRHELMachine(): failed to enable RDMA on RHEL machine")
		return false, err
	}

	// check if we need to install rdma-core package
	if isRDMAEnable {
		isRDMALoaded, err := h.RdmaIsLoaded()
		if err != nil {
			log.Log.Error(err, "EnableRDMAOnRHELMachine(): failed to check if RDMA kernel modules are loaded")
			return false, err
		}

		// if ib kernel module is not loaded trigger a loading
		if isRDMALoaded {
			err = h.TriggerUdevEvent()
			if err != nil {
				log.Log.Error(err, "EnableRDMAOnRHELMachine() failed to trigger udev event")
				return false, err
			}
		}
	}

	return true, nil
}

func (h *HostManager) EnableRDMAOnUbuntuMachine() (bool, error) {
	log.Log.Info("EnableRDMAOnUbuntuMachine(): enabling RDMA on RHEL machine")
	isRDMAEnable, err := h.EnableRDMA(ubuntuRDMAConditionFile, ubuntuRDMAServiceName, ubuntuPackageManager)
	if err != nil {
		log.Log.Error(err, "EnableRDMAOnUbuntuMachine(): failed to enable RDMA on Ubuntu machine")
		return false, err
	}

	// check if we need to install rdma-core package
	if isRDMAEnable {
		isRDMALoaded, err := h.RdmaIsLoaded()
		if err != nil {
			log.Log.Error(err, "EnableRDMAOnUbuntuMachine(): failed to check if RDMA kernel modules are loaded")
			return false, err
		}

		// if ib kernel module is not loaded trigger a loading
		if isRDMALoaded {
			err = h.TriggerUdevEvent()
			if err != nil {
				log.Log.Error(err, "EnableRDMAOnUbuntuMachine() failed to trigger udev event")
				return false, err
			}
		}
	}

	return true, nil
}

func (h *HostManager) IsRHELSystem() (bool, error) {
	log.Log.Info("IsRHELSystem(): checking for RHEL machine")
	path := redhatReleaseFile
	if !h.RunOnHost {
		path = pathlib.Join(hostPathFromDaemon, path)
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("IsRHELSystem() not a RHEL machine")
			return false, nil
		}

		log.Log.Error(err, "IsRHELSystem() failed to check for os release file", "path", path)
		return false, err
	}

	return true, nil
}

func (h *HostManager) IsCoreOS() (bool, error) {
	log.Log.Info("IsCoreOS(): checking for CoreOS machine")
	path := redhatReleaseFile
	if !h.RunOnHost {
		path = pathlib.Join(hostPathFromDaemon, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Log.Error(err, "IsCoreOS(): failed to read RHEL release file on path", "path", path)
		return false, err
	}

	if strings.Contains(string(data), "CoreOS") {
		return true, nil
	}

	return false, nil
}

func (h *HostManager) IsUbuntuSystem() (bool, error) {
	log.Log.Info("IsUbuntuSystem(): checking for Ubuntu machine")
	path := genericOSReleaseFile
	if !h.RunOnHost {
		path = pathlib.Join(hostPathFromDaemon, path)
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			log.Log.Error(nil, "IsUbuntuSystem() os-release on path doesn't exist", "path", path)
			return false, err
		}

		log.Log.Error(err, "IsUbuntuSystem() failed to check for os release file", "path", path)
		return false, err
	}

	stdout, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("grep -i --quiet 'ubuntu' %s", path))
	if err != nil && stderr.Len() != 0 {
		log.Log.Error(err, "IsUbuntuSystem(): failed to check for ubuntu operating system name in os-releasae file", "stderr", stderr.String())
		return false, fmt.Errorf(stderr.String())
	}

	if stdout.Len() > 0 {
		return true, nil
	}

	return false, nil
}

func (h *HostManager) RdmaIsLoaded() (bool, error) {
	log.Log.V(2).Info("RdmaIsLoaded()")
	chrootDefinition := getChrootExtension(h.RunOnHost)

	// check if the driver is already loaded in to the system
	_, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("grep --quiet '\\(^ib\\|^rdma\\)' <(%s lsmod)", chrootDefinition))
	if err != nil && stderr.Len() != 0 {
		log.Log.Error(err, "RdmaIsLoaded(): fail to check if ib and rdma kernel modules are loaded", "stderr", stderr.String())
		return false, fmt.Errorf(stderr.String())
	}

	if err != nil {
		return false, nil
	}

	return true, nil
}

func (h *HostManager) EnableRDMA(conditionFilePath, serviceName, packageManager string) (bool, error) {
	path := conditionFilePath
	if !h.RunOnHost {
		path = pathlib.Join(hostPathFromDaemon, path)
	}
	log.Log.Info("EnableRDMA(): checking for service file", "path", path)

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("EnableRDMA(): RDMA server doesn't exist")
			err = h.InstallRDMA(packageManager)
			if err != nil {
				log.Log.Error(err, "EnableRDMA() failed to install RDMA package")
				return false, err
			}

			err = h.TriggerUdevEvent()
			if err != nil {
				log.Log.Error(err, "EnableRDMA() failed to trigger udev event")
				return false, err
			}

			return false, nil
		}

		log.Log.Error(err, "EnableRDMA() failed to check for os release file", "path", path)
		return false, err
	}

	log.Log.Info("EnableRDMA(): service installed", "name", serviceName)
	return true, nil
}

func (h *HostManager) InstallRDMA(packageManager string) error {
	log.Log.Info("InstallRDMA(): installing RDMA")
	chrootDefinition := getChrootExtension(h.RunOnHost)

	stdout, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("%s %s install -y rdma-core", chrootDefinition, packageManager))
	if err != nil && stderr.Len() != 0 {
		log.Log.Error(err, "InstallRDMA(): failed to install RDMA package", "stdout", stdout.String(), "stderr", stderr.String())
		return err
	}

	return nil
}

func (h *HostManager) TriggerUdevEvent() error {
	log.Log.Info("TriggerUdevEvent(): installing RDMA")

	err := h.ReloadDriver("mlx4_en")
	if err != nil {
		return err
	}

	err = h.ReloadDriver("mlx5_core")
	if err != nil {
		return err
	}

	return nil
}

func (h *HostManager) ReloadDriver(driverName string) error {
	log.Log.Info("ReloadDriver(): reload driver", "name", driverName)
	chrootDefinition := getChrootExtension(h.RunOnHost)

	_, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("%s modprobe -r %s && %s modprobe %s", chrootDefinition, driverName, chrootDefinition, driverName))
	if err != nil && stderr.Len() != 0 {
		log.Log.Error(err, "InstallRDMA(): failed to reload kernel module",
			"name", driverName, "stderr", stderr.String())
		return err
	}

	return nil
}

func (h *HostManager) GetOSPrettyName() (string, error) {
	path := genericOSReleaseFile
	if !h.RunOnHost {
		path = pathlib.Join(hostPathFromDaemon, path)
	}

	log.Log.Info("GetOSPrettyName(): getting os name from os-release file")

	stdout, stderr, err := h.cmd.Run("/bin/sh", "-c", fmt.Sprintf("cat %s | grep PRETTY_NAME | cut -c 13-", path))
	if err != nil && stderr.Len() != 0 {
		log.Log.Error(err, "IsUbuntuSystem(): failed to check for ubuntu operating system name in os-releasae file", "stderr", stderr.String())
		return "", fmt.Errorf(stderr.String())
	}

	if stdout.Len() > 0 {
		return stdout.String(), nil
	}

	return "", fmt.Errorf("failed to find pretty operating system name")
}

func getChrootExtension(runOnHost bool) string {
	if !runOnHost {
		return fmt.Sprintf("chroot %s/host", utils.FilesystemRoot)
	}
	return utils.FilesystemRoot
}
