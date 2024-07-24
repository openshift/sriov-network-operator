package kernel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type kernel struct {
	utilsHelper utils.CmdInterface
}

func New(utilsHelper utils.CmdInterface) types.KernelInterface {
	return &kernel{utilsHelper: utilsHelper}
}

func (k *kernel) LoadKernelModule(name string, args ...string) error {
	log.Log.Info("LoadKernelModule(): try to load kernel module", "name", name, "args", args)
	chrootDefinition := utils.GetChrootExtension()
	cmdArgs := strings.Join(args, " ")

	// check if the driver is already loaded in to the system
	isLoaded, err := k.IsKernelModuleLoaded(name)
	if err != nil {
		log.Log.Error(err, "LoadKernelModule(): failed to check if kernel module is already loaded", "name", name)
	}
	if isLoaded {
		log.Log.Info("LoadKernelModule(): kernel module already loaded", "name", name)
		return nil
	}

	_, _, err = k.utilsHelper.RunCommand("/bin/sh", "-c", fmt.Sprintf("%s modprobe %s %s", chrootDefinition, name, cmdArgs))
	if err != nil {
		log.Log.Error(err, "LoadKernelModule(): failed to load kernel module with arguments", "name", name, "args", args)
		return err
	}
	return nil
}

func (k *kernel) IsKernelModuleLoaded(kernelModuleName string) (bool, error) {
	log.Log.Info("IsKernelModuleLoaded(): check if kernel module is loaded", "name", kernelModuleName)
	chrootDefinition := utils.GetChrootExtension()

	stdout, stderr, err := k.utilsHelper.RunCommand("/bin/sh", "-c", fmt.Sprintf("%s lsmod | grep \"^%s\"", chrootDefinition, kernelModuleName))
	if err != nil && len(stderr) != 0 {
		log.Log.Error(err, "IsKernelModuleLoaded(): failed to check if kernel module is loaded",
			"name", kernelModuleName, "stderr", stderr)
		return false, err
	}
	log.Log.V(2).Info("IsKernelModuleLoaded():", "stdout", stdout)
	if len(stderr) != 0 {
		log.Log.Error(err, "IsKernelModuleLoaded(): failed to check if kernel module is loaded", "name", kernelModuleName, "stderr", stderr)
		return false, fmt.Errorf(stderr)
	}

	if len(stdout) != 0 {
		log.Log.Info("IsKernelModuleLoaded(): kernel module already loaded", "name", kernelModuleName)
		return true, nil
	}

	return false, nil
}

func (k *kernel) TryEnableTun() {
	if err := k.LoadKernelModule("tun"); err != nil {
		log.Log.Error(err, "tryEnableTun(): TUN kernel module not loaded")
	}
}

func (k *kernel) TryEnableVhostNet() {
	if err := k.LoadKernelModule("vhost_net"); err != nil {
		log.Log.Error(err, "tryEnableVhostNet(): VHOST_NET kernel module not loaded")
	}
}

// GetCurrentKernelArgs This retrieves the kernel cmd line arguments
func (k *kernel) GetCurrentKernelArgs() (string, error) {
	path := consts.ProcKernelCmdLine
	if !vars.UsingSystemdMode {
		path = filepath.Join(consts.Host, path)
	}

	path = filepath.Join(vars.FilesystemRoot, path)
	cmdLine, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("GetCurrentKernelArgs(): Error reading %s: %v", path, err)
	}
	return string(cmdLine), nil
}

// IsKernelArgsSet This checks if the kernel cmd line is set properly. Please note that the same key could be repeated
// several times in the kernel cmd line. We can only ensure that the kernel cmd line has the key/val kernel arg that we set.
func (k *kernel) IsKernelArgsSet(cmdLine string, karg string) bool {
	elements := strings.Fields(cmdLine)
	for _, element := range elements {
		if element == karg {
			return true
		}
	}
	return false
}

// Unbind unbind driver for one device
func (k *kernel) Unbind(pciAddr string) error {
	log.Log.V(2).Info("Unbind(): unbind device driver for device", "device", pciAddr)
	return k.UnbindDriverByBusAndDevice(consts.BusPci, pciAddr)
}

// BindDpdkDriver bind dpdk driver for one device
// Bind the device given by "pciAddr" to the driver "driver"
func (k *kernel) BindDpdkDriver(pciAddr, driver string) error {
	log.Log.V(2).Info("BindDpdkDriver(): bind device to driver",
		"device", pciAddr, "driver", driver)
	if err := k.BindDriverByBusAndDevice(consts.BusPci, pciAddr, driver); err != nil {
		_, innerErr := os.Readlink(filepath.Join(vars.FilesystemRoot, consts.SysBusPciDevices, pciAddr, "iommu_group"))
		if innerErr != nil {
			log.Log.Error(err, "Could not read IOMMU group for device", "device", pciAddr)
			return fmt.Errorf(
				"cannot bind driver %s to device %s, make sure IOMMU is enabled in BIOS. %w", driver, pciAddr, innerErr)
		}
		return err
	}
	return nil
}

// BindDefaultDriver bind driver for one device
// Bind the device given by "pciAddr" to the default driver
func (k *kernel) BindDefaultDriver(pciAddr string) error {
	log.Log.V(2).Info("BindDefaultDriver(): bind device to default driver", "device", pciAddr)

	curDriver, err := getDriverByBusAndDevice(consts.BusPci, pciAddr)
	if err != nil {
		return err
	}
	if curDriver != "" {
		if !sriovnetworkv1.StringInArray(curDriver, vars.DpdkDrivers) {
			log.Log.V(2).Info("BindDefaultDriver(): device already bound to default driver",
				"device", pciAddr, "driver", curDriver)
			return nil
		}
		if err := k.UnbindDriverByBusAndDevice(consts.BusPci, pciAddr); err != nil {
			return err
		}
	}
	if err := setDriverOverride(consts.BusPci, pciAddr, ""); err != nil {
		return err
	}
	if err := probeDriver(consts.BusPci, pciAddr); err != nil {
		return err
	}
	return nil
}

// BindDriverByBusAndDevice binds device to the provided driver
// bus - the bus path in the sysfs, e.g. "pci" or "vdpa"
// device - the name of the device on the bus, e.g. 0000:85:1e.5 for PCI or vpda1 for VDPA
// driver - the name of the driver, e.g. vfio-pci or vhost_vdpa.
func (k *kernel) BindDriverByBusAndDevice(bus, device, driver string) error {
	log.Log.V(2).Info("BindDriverByBusAndDevice(): bind device to driver",
		"bus", bus, "device", device, "driver", driver)

	curDriver, err := getDriverByBusAndDevice(bus, device)
	if err != nil {
		return err
	}
	if curDriver != "" {
		if curDriver == driver {
			log.Log.V(2).Info("BindDriverByBusAndDevice(): device already bound to driver",
				"bus", bus, "device", device, "driver", driver)
			return nil
		}
		if err := k.UnbindDriverByBusAndDevice(bus, device); err != nil {
			return err
		}
	}
	if err := setDriverOverride(bus, device, driver); err != nil {
		return err
	}
	if err := bindDriver(bus, device, driver); err != nil {
		return err
	}
	return setDriverOverride(bus, device, "")
}

// Workaround function to handle a case where the vf default driver is stuck and not able to create the vf kernel interface.
// This function unbind the VF from the default driver and try to bind it again
// bugzilla: https://bugzilla.redhat.com/show_bug.cgi?id=2045087
func (k *kernel) RebindVfToDefaultDriver(vfAddr string) error {
	log.Log.Info("RebindVfToDefaultDriver()", "vf", vfAddr)
	if err := k.Unbind(vfAddr); err != nil {
		return err
	}
	if err := k.BindDefaultDriver(vfAddr); err != nil {
		log.Log.Error(err, "RebindVfToDefaultDriver(): fail to bind default driver", "device", vfAddr)
		return err
	}

	log.Log.Info("RebindVfToDefaultDriver(): workaround implemented", "vf", vfAddr)
	return nil
}

func (k *kernel) UnbindDriverIfNeeded(vfAddr string, isRdma bool) error {
	if isRdma {
		log.Log.Info("UnbindDriverIfNeeded(): unbinding driver", "device", vfAddr)
		if err := k.Unbind(vfAddr); err != nil {
			return err
		}
		log.Log.Info("UnbindDriverIfNeeded(): unbounded driver", "device", vfAddr)
	}
	return nil
}

// UnbindDriverByBusAndDevice unbind device identified by bus and device ID from the driver
// bus - the bus path in the sysfs, e.g. "pci" or "vdpa"
// device - the name of the device on the bus, e.g. 0000:85:1e.5 for PCI or vpda1 for VDPA
func (k *kernel) UnbindDriverByBusAndDevice(bus, device string) error {
	log.Log.V(2).Info("UnbindDriverByBusAndDevice(): unbind device driver for device", "bus", bus, "device", device)
	driver, err := getDriverByBusAndDevice(bus, device)
	if err != nil {
		return err
	}
	if driver == "" {
		log.Log.V(2).Info("UnbindDriverByBusAndDevice(): device has no driver", "bus", bus, "device", device)
		return nil
	}
	return unbindDriver(bus, device, driver)
}

func (k *kernel) HasDriver(pciAddr string) (bool, string) {
	driver, err := getDriverByBusAndDevice(consts.BusPci, pciAddr)
	if err != nil {
		log.Log.V(2).Info("HasDriver(): device driver is empty for device", "device", pciAddr)
		return false, ""
	}
	if driver != "" {
		log.Log.V(2).Info("HasDriver(): device driver for device", "device", pciAddr, "driver", driver)
		return true, driver
	}
	return false, ""
}

// GetDriverByBusAndDevice returns driver for the device or error.
// returns "", nil if the device has no driver.
// bus - the bus path in the sysfs, e.g. "pci" or "vdpa"
// device - the name of the device on the bus, e.g. 0000:85:1e.5 for PCI or vpda1 for VDPA
func (k *kernel) GetDriverByBusAndDevice(bus, device string) (string, error) {
	log.Log.V(2).Info("GetDriverByBusAndDevice(): get driver for device", "bus", bus, "device", device)
	return getDriverByBusAndDevice(bus, device)
}

// CheckRDMAEnabled returns true if RDMA modules are loaded on host
func (k *kernel) CheckRDMAEnabled() (bool, error) {
	log.Log.V(2).Info("CheckRDMAEnabled()")
	chrootDefinition := utils.GetChrootExtension()

	_, stderr, mlx5Err := k.utilsHelper.RunCommand("/bin/sh", "-c", fmt.Sprintf("%s lsmod | grep --quiet 'mlx5_core'", chrootDefinition))
	if mlx5Err != nil && len(stderr) != 0 {
		log.Log.Error(mlx5Err, "CheckRDMAEnabled(): failed to check for kernel module 'mlx5_core'", "stderr", stderr)
		return false, fmt.Errorf(stderr)
	}

	if mlx5Err != nil {
		log.Log.Error(nil, "CheckRDMAEnabled(): no RDMA capable devices")
		return false, nil
	}
	return k.rdmaModulesAreLoaded()
}

func (k *kernel) rdmaModulesAreLoaded() (bool, error) {
	log.Log.V(2).Info("rdmaModulesAreLoaded()")
	chrootDefinition := utils.GetChrootExtension()

	// check if the driver is already loaded in to the system
	_, stderr, err := k.utilsHelper.RunCommand("/bin/sh", "-c", fmt.Sprintf("%s lsmod | grep --quiet '\\(^ib\\|^rdma\\)'", chrootDefinition))
	if err != nil && len(stderr) != 0 {
		log.Log.Error(err, "rdmaModulesAreLoaded(): fail to check if ib and rdma kernel modules are loaded", "stderr", stderr)
		return false, fmt.Errorf(stderr)
	}

	if err != nil {
		log.Log.Error(nil, "rdmaModulesAreLoaded(): RDMA modules are not loaded, you may need to install rdma-core package")
		return false, nil
	}
	log.Log.V(2).Info("rdmaModulesAreLoaded(): RDMA modules are loaded")
	return true, nil
}

// IsKernelLockdownMode returns true when kernel lockdown mode is enabled
// TODO: change this to return error
func (k *kernel) IsKernelLockdownMode() bool {
	path := utils.GetHostExtension()
	path = filepath.Join(path, "/sys/kernel/security/lockdown")

	stdout, stderr, err := k.utilsHelper.RunCommand("cat", path)
	log.Log.V(2).Info("IsKernelLockdownMode()", "output", stdout, "error", err)
	if err != nil {
		log.Log.Error(err, "IsKernelLockdownMode(): failed to check for lockdown file", "stderr", stderr)
		return false
	}
	return strings.Contains(stdout, "[integrity]") || strings.Contains(stdout, "[confidentiality]")
}

// returns driver for device on the bus
func getDriverByBusAndDevice(bus, device string) (string, error) {
	driverLink := filepath.Join(vars.FilesystemRoot, consts.SysBus, bus, "devices", device, "driver")
	driverInfo, err := os.Readlink(driverLink)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Log.V(2).Info("getDriverByBusAndDevice(): driver path for device not exist", "bus", bus, "device", device, "driver", driverInfo)
			return "", nil
		}
		log.Log.Error(err, "getDriverByBusAndDevice(): error getting driver info for device", "bus", bus, "device", device)
		return "", err
	}
	log.Log.V(2).Info("getDriverByBusAndDevice(): driver for device", "bus", bus, "device", device, "driver", driverInfo)
	return filepath.Base(driverInfo), nil
}

// binds device to the provide driver
func bindDriver(bus, device, driver string) error {
	log.Log.V(2).Info("bindDriver(): bind to driver", "bus", bus, "device", device, "driver", driver)
	bindPath := filepath.Join(vars.FilesystemRoot, consts.SysBus, bus, "drivers", driver, "bind")
	err := os.WriteFile(bindPath, []byte(device), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "bindDriver(): failed to bind driver", "bus", bus, "device", device, "driver", driver)
		return err
	}
	return nil
}

// unbind device from the driver
func unbindDriver(bus, device, driver string) error {
	log.Log.V(2).Info("unbindDriver(): unbind from driver", "bus", bus, "device", device, "driver", driver)
	unbindPath := filepath.Join(vars.FilesystemRoot, consts.SysBus, bus, "drivers", driver, "unbind")
	err := os.WriteFile(unbindPath, []byte(device), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "unbindDriver(): failed to unbind driver", "bus", bus, "device", device, "driver", driver)
		return err
	}
	return nil
}

// probes driver for device on the bus
func probeDriver(bus, device string) error {
	log.Log.V(2).Info("probeDriver(): drivers probe", "bus", bus, "device", device)
	probePath := filepath.Join(vars.FilesystemRoot, consts.SysBus, bus, "drivers_probe")
	err := os.WriteFile(probePath, []byte(device), os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "probeDriver(): failed to trigger driver probe", "bus", bus, "device", device)
		return err
	}
	return nil
}

// set driver override for the bus/device,
// resets override if override arg is "",
// if device doesn't support overriding (has no driver_override path), does nothing
func setDriverOverride(bus, device, override string) error {
	driverOverridePath := filepath.Join(vars.FilesystemRoot, consts.SysBus, bus, "devices", device, "driver_override")
	if _, err := os.Stat(driverOverridePath); err != nil {
		if os.IsNotExist(err) {
			log.Log.V(2).Info("setDriverOverride(): device doesn't support driver override, skip", "bus", bus, "device", device)
			return nil
		}
		return err
	}
	var overrideData []byte
	if override != "" {
		log.Log.V(2).Info("setDriverOverride(): configure driver override for device", "bus", bus, "device", device, "driver", override)
		overrideData = []byte(override)
	} else {
		log.Log.V(2).Info("setDriverOverride(): reset driver override for device", "bus", bus, "device", device)
		overrideData = []byte("\x00")
	}
	err := os.WriteFile(driverOverridePath, overrideData, os.ModeAppend)
	if err != nil {
		log.Log.Error(err, "setDriverOverride(): fail to write driver_override for device",
			"bus", bus, "device", device, "driver", override)
		return err
	}
	return nil
}
