package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	testnetns "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/netns"
	"github.com/vishvananda/netns"
)

const (
	pfPciAddrFlag     = "pfpciaddress"
	netNsPathFlag     = "netnspath"
	pollPeriodFlag    = "millisecpollperiod"
	defaultPollPeriod = 400 * time.Millisecond
	sysBusPci         = "/sys/bus/pci/devices"
)

//main This software was created to address a problem when testing with KinD. A physical function (PF) is moved to a test
//container with its own network namespace (netns). During a test, virtual functions (VF) are created & destroyed.
// When a PF which is not in the root netns when VFs are created, they are created in the root netns and not in
//the netns of the PF. This software ensures they are kept in the correct netns by polling for
//VFs and setting the correct netns when discovered. A PCI address of the PF is required as it is a persistent identifier
//unlike a link name.
func main() {
	var err error
	pollPeriod := defaultPollPeriod
	pfPciAddr := flag.String(pfPciAddrFlag, "", "physical function PCI address e.g 0000:01:00.0")
	netnsPath := flag.String(netNsPathFlag, "", "network namespace path e.g. /var/run/docker/netns/sdfsdfe344")
	mPollPeriod := flag.Int(pollPeriodFlag, 0, "millisecond polling period for watching devices netns."+
		" Default is 400ms interval")
	flag.Parse()

	if *pfPciAddr == "" || *netnsPath == "" {
		log.Fatalf("specify flags '%s' & '%s'\n", pfPciAddrFlag, netNsPathFlag)
	}

	if *mPollPeriod > 0 {
		pollPeriod = time.Duration(*mPollPeriod) * time.Millisecond
	}

	pfPciDir := filepath.Join(sysBusPci, *pfPciAddr)
	if _, err := os.Lstat(pfPciDir); err != nil {
		log.Fatalf("failed to find PCI device at '%s': '%s'", pfPciDir, err.Error())
	}

	targetNetNs, err := netns.GetFromPath(*netnsPath)
	if err != nil {
		log.Fatalf("failed to get target network namespace: '%s'", err.Error())
	}
	defer targetNetNs.Close()

	if err = testnetns.SetLinkNetNs(*pfPciAddr, targetNetNs); err != nil {
		log.Printf("unable to set physical function '%s' network namespace: '%s'\ncontinuing..", *pfPciAddr,
			err.Error())
	}

	term := make(chan os.Signal)
	signal.Notify(term, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	for {
		select {
		case <-term:
			log.Print("quit signal received")
			return
		case <-time.Tick(pollPeriod):
			if err = testnetns.SetVfNetNs(*pfPciAddr, targetNetNs); err != nil {
				log.Fatal(err.Error())
			}
		}
	}
}
