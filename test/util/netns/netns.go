package netns

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const (
	sysBusPci       = "/sys/bus/pci/devices"
	sriovNumVfsFile = "sriov_numvfs"
)

// SetPfVfLinkNetNs requires physical function (PF) PCI address and a string to a network namespace in which to add
// any associated virtual functions (VF). VFs must be attached to kernel driver to provide links. Attaching VFs to
// vfio-pci driver is not supported. PF is set to target network namespace.
// Polling interval is required and this period will determine how often the VFs Links are checked to ensure they are in
// the target network namespace. Two channels are required - one for informing the func to end and one to inform
// the caller of an error or if the function has ended. It is this func responsibility to cleanup the done channel and
// callers responsibility to cleanup quit channel.
func SetPfVfLinkNetNs(pfPciAddr, netNsPath string, pollInterval time.Duration, quitCh chan bool, doneCh chan error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var errL []error
	//merge any errors found during execution, send through channel, and close it.
	defer func() {
		if len(errL) != 0 {
			errMsg := ""
			for i, err := range errL {
				errMsg = errMsg + fmt.Sprintf("error %d) '%s'", i+1, err.Error())
			}
			doneCh <- fmt.Errorf("%s", errMsg)
		}
		close(doneCh)
	}()

	if pfPciAddr == "" || netNsPath == "" {
		errL = append(errL, fmt.Errorf("SetPfVfLinkNetNs(): specify PF PCI address and/or netns path '%s' & '%s'",
			pfPciAddr, netNsPath))
		return
	}

	pfPciDir := filepath.Join(sysBusPci, pfPciAddr)
	if _, err := os.Lstat(pfPciDir); err != nil {
		errL = append(errL, fmt.Errorf("SetPfVfLinkNetNs(): failed to find PCI device at '%s': '%s'", pfPciDir,
			err.Error()))
		return
	}

	targetNetNs, err := netns.GetFromPath(netNsPath)
	if err != nil {
		errL = append(errL, fmt.Errorf("SetPfVfLinkNetNs(): failed to get target network namespace: '%s'",
			err.Error()))
		return
	}
	defer targetNetNs.Close()
	// if failure to set PF netns, emit error, assume its in correct netns and continue
	if err = setLinkNetNs(pfPciAddr, targetNetNs); err != nil {
		errL = append(errL, fmt.Errorf("SetPfVfLinkNetNs(): unable to set physical function '%s' network namespace: '%s'", pfPciAddr,
			err.Error()))
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-quitCh:
			return
		case <-ticker.C:
			if err := setVfNetNs(pfPciAddr, targetNetNs); err != nil {
				//save errors for returning but continue
				errL = append(errL, err)
			}
		}
	}
}

// setVfNetNs requires physical function (PF) PCI address and a handle to a network namespace in which to add
// any associated virtual functions (VF). If no VFs are found, no error is returned.
func setVfNetNs(pfPciAddr string, targetNetNs netns.NsHandle) error {
	var (
		err      error
		link     netlink.Link
		vfNetDir string
	)
	numVfsFile := filepath.Join(sysBusPci, pfPciAddr, sriovNumVfsFile)
	if _, err := os.Lstat(numVfsFile); err != nil {
		return fmt.Errorf("setVfNetNs(): unable to open '%s' from device with PCI address '%s': '%s", numVfsFile, pfPciAddr,
			err.Error())
	}

	data, err := os.ReadFile(numVfsFile)
	if err != nil {
		return fmt.Errorf("setVfNetNs(): failed to read '%s' from device with PCI address  '%s': '%s", numVfsFile, pfPciAddr,
			err.Error())
	}

	if len(data) == 0 {
		return fmt.Errorf("setVfNetNs(): no data in file '%s'", numVfsFile)
	}

	vfTotal, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("setVfNetNs(): unable to convert file '%s' to integer: '%s'", numVfsFile, err.Error())
	}

	for vfNo := 0; vfNo <= vfTotal; vfNo++ {
		vfNetDir = filepath.Join(sysBusPci, pfPciAddr, fmt.Sprintf("virtfn%d", vfNo), "net")
		_, err = os.Lstat(vfNetDir)
		if err != nil {
			continue
		}

		fInfos, err := os.ReadDir(vfNetDir)
		if err != nil {
			return fmt.Errorf("setVfNetNs(): failed to read '%s': '%s'", vfNetDir, err.Error())
		}
		if len(fInfos) == 0 {
			continue
		}

		for _, f := range fInfos {
			link, err = netlink.LinkByName(f.Name())
			if err != nil {
				return fmt.Errorf("setVfNetNs(): unable to get link with name '%s' from directory '%s': '%s'", f.Name(),
					vfNetDir, err.Error())
			}
			if err = netlink.LinkSetNsFd(link, int(targetNetNs)); err != nil {
				return fmt.Errorf("setVfNetNs(): failed to set link '%s' netns: '%s'", f.Name(), err.Error())
			}
		}
	}
	return nil
}

// setLinkNetNs attempts to create a link object from PCI address and change the network namespace. Arg pciAddr must have
// an associated interface name in the current network namespace or an error is thrown.
func setLinkNetNs(pciAddr string, targetNetNs netns.NsHandle) error {
	var err error
	netDir := filepath.Join(sysBusPci, pciAddr, "net")
	if _, err := os.Lstat(netDir); err != nil {
		return fmt.Errorf("setLinkNetNs(): unable to find directory '%s': '%s'", netDir, err.Error())
	}
	fInfos, err := os.ReadDir(netDir)
	if err != nil {
		return fmt.Errorf("setLinkNetNs(): failed to read '%s': '%s'", netDir, err.Error())
	}
	if len(fInfos) == 0 {
		return fmt.Errorf("setLinkNetNs(): no files found at directory '%s'", netDir)
	}
	link, err := netlink.LinkByName(fInfos[0].Name())
	if err != nil {
		return fmt.Errorf("setLinkNetNs(): failed to create link object by name '%s' from files at '%s': '%s'", fInfos[0].Name(),
			netDir, err.Error())
	}
	err = netlink.LinkSetNsFd(link, int(targetNetNs))
	if err != nil {
		return fmt.Errorf("setLinkNetNs(): failed to set link '%s' netns: '%s'", link.Attrs().Name, err.Error())
	}
	return nil
}
