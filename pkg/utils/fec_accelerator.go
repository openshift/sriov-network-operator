package utils

import (
	"bytes"
	"fmt"
	"github.com/golang/glog"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	dputils "github.com/intel/sriov-network-device-plugin/pkg/utils"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
)

const acceleratorConfigPath = "/bindata/accel-config"

var acceleratorIntelFecDeviceTypeById = map[string]string{
	"1172:5052": "FPGA_LTE",
	"8086:0D8F": "FPGA_5GNR",
}

func configSriovIntelFecAcceleratorDevice(card *sriovnetworkv1.Card, accelStatus *sriovnetworkv1.AcceleratorExt) error {
	glog.V(2).Infof("configSriovIntelFecAcceleratorDevice(): config interface %s with %v", card.PciAddress, card)
	var err error
	if card.NumVfs > accelStatus.TotalVfs {
		err := fmt.Errorf("cannot config SRIOV device: NumVfs is larger than TotalVfs")
		glog.Errorf("configSriovIntelFecAcceleratorDevice(): fail to set NumVfs for device %s: %v", card.PciAddress, err)
		return err
	}

	exit, err := Chroot("/host")
	if err != nil {
		return fmt.Errorf("failed to chroot into host file system to attach PF to pci-pf driver: %v", err)
	}

	if err := BindDpdkDriver(card.PciAddress, "pci-pf-stub"); err != nil {
		glog.Warningf("configSriovNetworkDevice(): fail to bind driver %s for device %s", "pci-pf-stub", card.PciAddress)
		return err
	}
	if err := exit(); err != nil {
		return err
	}

	// Write the config file for the config_bbdev
	err = ioutil.WriteFile(fmt.Sprintf("/%s/%s.cfg.tmp", acceleratorConfigPath, card.PciAddress), []byte(card.Config), 0644)
	if err != nil {
		return err
	}

	args := []string{card.DeviceName,
		"-c",
		fmt.Sprintf("%s/%s.cfg.tmp", acceleratorConfigPath, card.PciAddress),
		"-p",
		card.PciAddress,
		"-f",
		fmt.Sprintf("%d", card.NumVfs)}
	output, err := runCommand("/usr/local/sbin/config_bbdev", args...)
	if err != nil {
		return err
	}

	if !strings.Contains(output, "configuration complete") {
		return fmt.Errorf("config_bbdev configuration failed: %s", output)
	}

	exit, err = Chroot("/host")
	if err != nil {
		return err
	}
	vfAddrs, err := dputils.GetVFList(card.PciAddress)
	if err != nil {
		glog.Warningf("configSriovIntelFecAcceleratorDevice(): unable to parse VFs for device %+v %q", card.PciAddress, err)
	}

	for _, addr := range vfAddrs {
		if err := BindDpdkDriver(addr, "vfio-pci"); err != nil {
			glog.Warningf("configSriovNetworkDevice(): fail to bind driver %s for device %s", "vfio-pci", addr)
			return err
		}
	}
	if err := exit(); err != nil {
		return err
	}

	err = os.Rename(fmt.Sprintf("%s/%s.cfg.tmp", acceleratorConfigPath, card.PciAddress), fmt.Sprintf("%s/%s.cfg", acceleratorConfigPath, card.PciAddress))
	if err != nil {
		return fmt.Errorf("failed to move the bbdev configuration file for the fpga %s: %v", card.PciAddress, err)
	}

	return nil
}

func resetSriovIntelFecAcceleratorDevice(accelStatus sriovnetworkv1.AcceleratorExt) error {
	glog.V(2).Infof("resetSriovIntelFecAcceleratorDevice(): reset SRIOV device %s", accelStatus.PciAddress)
	if _, err := os.Stat(fmt.Sprintf("/%s/%s.cfg", acceleratorConfigPath, "empty")); os.IsNotExist(err) {
		emptyFile, err := os.Create(fmt.Sprintf("/%s/%s.cfg", acceleratorConfigPath, "empty"))
		if err != nil {
			return fmt.Errorf("resetSriovIntelFecAcceleratorDevice(): failed to create empty config file: %v", err)
		}

		err = emptyFile.Close()
		if err != nil {
			return fmt.Errorf("resetSriovIntelFecAcceleratorDevice(): failed to close the empty config file: %v", err)
		}
	}

	deviceName, ok := acceleratorIntelFecDeviceTypeById[fmt.Sprintf("%s:%s", accelStatus.Vendor, accelStatus.DeviceID)]
	if !ok {
		return fmt.Errorf("failed to find device name for the config bbdev for vendor %s and deviceID %s", accelStatus.Vendor, accelStatus.DeviceID)
	}

	args := []string{deviceName,
		"-c",
		fmt.Sprintf("%s/%s.cfg", acceleratorConfigPath, "empty"),
		"-p",
		accelStatus.PciAddress,
		"-f",
		"0"}
	output, err := runCommand("/usr/local/sbin/config_bbdev", args...)
	if err != nil {
		return err
	}

	if !strings.Contains(output, "configuration complete") {
		return fmt.Errorf("config_bbdev configuration failed: %s", output)
	}

	if _, err := os.Stat(fmt.Sprintf("%s/%s.cfg.tmp", acceleratorConfigPath, accelStatus.PciAddress)); err == nil {
		err = os.Remove(fmt.Sprintf("%s/%s.cfg.tmp", acceleratorConfigPath, accelStatus.PciAddress))
		return err
	}

	if _, err := os.Stat(fmt.Sprintf("%s/%s.cfg", acceleratorConfigPath, accelStatus.PciAddress)); err == nil {
		err = os.Remove(fmt.Sprintf("%s/%s.cfg", acceleratorConfigPath, accelStatus.PciAddress))
		return err
	}

	return nil
}

func runCommand(command string, args ...string) (string, error) {
	glog.Infof("runCommand(): %s %v", command, args)
	var stdout, stderr bytes.Buffer

	cmd := exec.Command(command, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	glog.V(2).Infof("runCommand(): %s, %v", command, args)
	err := cmd.Run()
	glog.V(2).Infof("runCommand(): %s", stdout.String())
	return stdout.String(), err
}
