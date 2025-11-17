package openstack

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/jaypipes/ghw"
	"github.com/jaypipes/ghw/pkg/net"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// GetOpenstackData gets the metadata and network_data
func getOpenstackData(mountConfigDrive bool) (metaData *OSPMetaData, networkData *OSPNetworkData, err error) {
	metaData, networkData, err = getOpenstackDataFromConfigDrive(mountConfigDrive)
	if err != nil {
		log.Log.Error(err, "GetOpenStackData(): non-fatal error getting OpenStack data from config drive")
		metaData, networkData, err = getOpenstackDataFromMetadataService()
		if err != nil {
			return metaData, networkData, fmt.Errorf("GetOpenStackData(): error getting OpenStack data: %w", err)
		}
	}

	// We can't rely on the PCI address from the metadata so we will lookup the real PCI address
	// for the NIC that matches the MAC address.
	//
	// Libvirt/QEMU cannot guarantee that the address specified in the XML will match the address seen by the guest.
	// This is a well known limitation: https://libvirt.org/pci-addresses.html
	// When using the q35 machine type, it highlights this issue due to the change from using PCI to PCI-E bus for virtual devices.
	//
	// With that said, the PCI value in Nova Metadata is a best effort hint due to the limitations mentioned above. Therefore
	// we will lookup the real PCI address for the NIC that matches the MAC address.
	netInfo, err := ghw.Network()
	if err != nil {
		return metaData, networkData, fmt.Errorf("GetOpenStackData(): error getting network info: %w", err)
	}
	for i, device := range metaData.Devices {
		realPCIAddr, err := getPCIAddressFromMACAddress(device.Mac, netInfo.NICs)
		if err != nil {
			// If we can't find the PCI address, we will just print a warning, return the data as is with no error.
			// In the future, we'll want to drain the node if sno-initial-node-state.json doesn't exist when daemon is restarted and when we have SR-IOV
			// allocated devices already.
			log.Log.Error(err, "Warning GetOpenstackData(): error getting PCI address for device",
				"device-mac", device.Mac)
			return metaData, networkData, nil
		}
		if realPCIAddr != device.Address {
			log.Log.V(2).Info("GetOpenstackData(): PCI address for device does not match Nova metadata value, it'll be overwritten",
				"device-mac", device.Mac,
				"current-address", device.Address,
				"overwrite-address", realPCIAddr)
			metaData.Devices[i].Address = realPCIAddr
		}
	}

	return metaData, networkData, err
}

// getConfigDriveDevice returns the config drive device which was found
func getConfigDriveDevice() (string, error) {
	dev := "/dev/disk/by-label/" + configDriveLabel
	if _, err := os.Stat(dev); os.IsNotExist(err) {
		out, err := exec.Command(
			"blkid", "-l",
			"-t", "LABEL="+configDriveLabel,
			"-o", "device",
		).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("unable to run blkid: %v", err)
		}
		dev = strings.TrimSpace(string(out))
	}
	log.Log.Info("found config drive device", "device", dev)
	return dev, nil
}

// mountConfigDriveDevice mounts the config drive and return the path
func mountConfigDriveDevice(device string) (string, error) {
	if device == "" {
		return "", fmt.Errorf("device is empty")
	}
	tmpDir, err := os.MkdirTemp("", "sriov-configdrive")
	if err != nil {
		return "", fmt.Errorf("error creating temp directory: %w", err)
	}
	cmd := exec.Command("mount", "-o", "ro", "-t", "auto", device, tmpDir)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error mounting config drive: %w", err)
	}
	log.Log.V(2).Info("mounted config drive device", "device", device, "path", tmpDir)
	return tmpDir, nil
}

// ummountConfigDriveDevice ummounts the config drive device
func ummountConfigDriveDevice(path string) error {
	if path == "" {
		return fmt.Errorf("path is empty")
	}
	cmd := exec.Command("umount", path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error umounting config drive: %w", err)
	}
	log.Log.V(2).Info("umounted config drive", "path", path)
	return nil
}

// getOpenstackDataFromConfigDrive reads the meta_data and network_data files
func getOpenstackDataFromConfigDrive(mountConfigDrive bool) (metaData *OSPMetaData, networkData *OSPNetworkData, err error) {
	metaData = &OSPMetaData{}
	networkData = &OSPNetworkData{}
	var configDrivePath string
	log.Log.Info("reading OpenStack meta_data from config-drive")
	var metadataf *os.File
	ospMetaDataFilePath := ospMetaDataFile
	if mountConfigDrive {
		configDriveDevice, err := getConfigDriveDevice()
		if err != nil {
			return metaData, networkData, fmt.Errorf("error finding config drive device: %w", err)
		}
		configDrivePath, err = mountConfigDriveDevice(configDriveDevice)
		if err != nil {
			return metaData, networkData, fmt.Errorf("error mounting config drive device: %w", err)
		}
		defer func() {
			if e := ummountConfigDriveDevice(configDrivePath); err == nil && e != nil {
				err = fmt.Errorf("error umounting config drive device: %w", e)
			}
			if e := os.Remove(configDrivePath); err == nil && e != nil {
				err = fmt.Errorf("error removing temp directory %s: %w", configDrivePath, e)
			}
		}()
		ospMetaDataFilePath = filepath.Join(configDrivePath, ospMetaDataBaseDir, ospMetaDataJSON)
		ospNetworkDataFile = filepath.Join(configDrivePath, ospMetaDataBaseDir, ospNetworkDataJSON)
	}
	metadataf, err = os.Open(ospMetaDataFilePath)
	if err != nil {
		return metaData, networkData, fmt.Errorf("error opening file %s: %w", ospMetaDataFilePath, err)
	}
	defer func() {
		if e := metadataf.Close(); err == nil && e != nil {
			err = fmt.Errorf("error closing file %s: %w", ospMetaDataFilePath, e)
		}
	}()
	if err = json.NewDecoder(metadataf).Decode(&metaData); err != nil {
		return metaData, networkData, fmt.Errorf("error unmarshalling metadata from file %s: %w", ospMetaDataFilePath, err)
	}

	log.Log.Info("reading OpenStack network_data from config-drive")
	var networkDataf *os.File
	ospNetworkDataFilePath := ospNetworkDataFile
	networkDataf, err = os.Open(ospNetworkDataFilePath)
	if err != nil {
		return metaData, networkData, fmt.Errorf("error opening file %s: %w", ospNetworkDataFilePath, err)
	}
	defer func() {
		if e := networkDataf.Close(); err == nil && e != nil {
			err = fmt.Errorf("error closing file %s: %w", ospNetworkDataFilePath, e)
		}
	}()
	if err = json.NewDecoder(networkDataf).Decode(&networkData); err != nil {
		return metaData, networkData, fmt.Errorf("error unmarshalling metadata from file %s: %w", ospNetworkDataFilePath, err)
	}
	return metaData, networkData, err
}

func getBodyFromURL(url string) ([]byte, error) {
	log.Log.V(2).Info("Getting body from", "url", url)
	resp, err := retryablehttp.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return rawBytes, nil
}

// getOpenstackDataFromMetadataService fetchs the metadata and network_data from the metadata service
func getOpenstackDataFromMetadataService() (metaData *OSPMetaData, networkData *OSPNetworkData, err error) {
	metaData = &OSPMetaData{}
	networkData = &OSPNetworkData{}
	log.Log.Info("getting OpenStack meta_data from metadata server")
	metaDataRawBytes, err := getBodyFromURL(ospMetaDataURL)
	if err != nil {
		return metaData, networkData, fmt.Errorf("error getting OpenStack meta_data from %s: %v", ospMetaDataURL, err)
	}
	err = json.Unmarshal(metaDataRawBytes, metaData)
	if err != nil {
		return metaData, networkData, fmt.Errorf("error unmarshalling raw bytes %v from %s", err, ospMetaDataURL)
	}

	log.Log.Info("getting OpenStack network_data from metadata server")
	networkDataRawBytes, err := getBodyFromURL(ospNetworkDataURL)
	if err != nil {
		return metaData, networkData, fmt.Errorf("error getting OpenStack network_data from %s: %v", ospNetworkDataURL, err)
	}
	err = json.Unmarshal(networkDataRawBytes, networkData)
	if err != nil {
		return metaData, networkData, fmt.Errorf("error unmarshalling raw bytes %v from %s", err, ospNetworkDataURL)
	}
	return metaData, networkData, nil
}

// getPCIAddressFromMACAddress returns the PCI address of a device given its MAC address
func getPCIAddressFromMACAddress(macAddress string, nics []*net.NIC) (string, error) {
	var pciAddress string
	for _, nic := range nics {
		if strings.EqualFold(nic.MacAddress, macAddress) {
			if pciAddress == "" {
				pciAddress = *nic.PCIAddress
			} else {
				return "", fmt.Errorf("more than one device found with MAC address %s is unsupported", macAddress)
			}
		}
	}

	if pciAddress != "" {
		return pciAddress, nil
	}

	return "", fmt.Errorf("no device found with MAC address %s", macAddress)
}
