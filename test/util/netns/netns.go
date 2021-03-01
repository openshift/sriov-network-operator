package netns

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const (
	sysBusPci       = "/sys/bus/pci/devices"
	sriovNumVfsFile = "sriov_numvfs"
)

//SetVfNetNs requires physical function (PF) PCI address and a handle to a network namespace in which to add
//any associated virtual functions (VF). If no VFs are found, no error is returned.
func SetVfNetNs(pfPciAddr string, targetNetNs netns.NsHandle) error {
	var (
		err      error
		link     netlink.Link
		vfNetDir string
	)
	numVfsFile := filepath.Join(sysBusPci, pfPciAddr, sriovNumVfsFile)
	if _, err := os.Lstat(numVfsFile); err != nil {
		return fmt.Errorf("unable to open '%s' from device with PCI address '%s': '%s", numVfsFile, pfPciAddr,
			err.Error())
	}

	data, err := ioutil.ReadFile(numVfsFile)
	if err != nil {
		return fmt.Errorf("failed to read '%s' from device with PCI address  '%s': '%s", numVfsFile, pfPciAddr,
			err.Error())
	}

	if len(data) == 0 {
		return fmt.Errorf("no data in file '%s'", numVfsFile)
	}

	vfTotal, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("unable to convert file '%s' to integer: '%s'", numVfsFile, err.Error())
	}

	for vfNo := 0; vfNo <= vfTotal; vfNo++ {
		vfNetDir = filepath.Join(sysBusPci, pfPciAddr, fmt.Sprintf("virtfn%d", vfNo), "net")
		_, err = os.Lstat(vfNetDir)
		if err != nil {
			continue
		}

		fInfos, err := ioutil.ReadDir(vfNetDir)
		if err != nil {
			return fmt.Errorf("failed to read '%s': '%s'", vfNetDir, err.Error())
		}
		if len(fInfos) == 0 {
			continue
		}

		for _, f := range fInfos {
			link, err = netlink.LinkByName(f.Name())
			if err != nil {
				return fmt.Errorf("unable to create link with name '%s' from directory '%s': '%s'", f.Name(),
					vfNetDir, err.Error())
			}
			if err = netlink.LinkSetNsFd(link, int(targetNetNs)); err != nil {
				return fmt.Errorf("failed to set link '%s' to netns: '%s'", f.Name(), err.Error())
			}
		}
	}
	return nil
}

//SetLinkNetNs attempts to create a link object from PCI address and change the network namespace. Arg pciAddr must have
//an associated interface name in the current network namespace or an error is thrown.
func SetLinkNetNs(pciAddr string, targetNetNs netns.NsHandle) error {
	var err error
	netDir := filepath.Join(sysBusPci, pciAddr, "net")
	if _, err := os.Lstat(netDir); err != nil {
		return fmt.Errorf("unable to find directory '%s': '%s'", netDir, err.Error())
	}
	fInfos, err := ioutil.ReadDir(netDir)
	if err != nil {
		return fmt.Errorf("failed to read '%s': '%s'", netDir, err.Error())
	}
	if len(fInfos) == 0 {
		return fmt.Errorf("no files found at directory '%s'", netDir)
	}
	link, err := netlink.LinkByName(fInfos[0].Name())
	if err != nil {
		return fmt.Errorf("failed to create link object by name '%s' from files at '%s': '%s'", fInfos[0].Name(),
			netDir, err.Error())
	}
	err = netlink.LinkSetNsFd(link, int(targetNetNs))
	if err != nil {
		return fmt.Errorf("failed to set link '%s' netns: '%s'", link.Attrs().Name, err.Error())
	}
	return nil
}
