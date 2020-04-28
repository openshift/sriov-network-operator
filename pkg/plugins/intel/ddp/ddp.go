package ddp

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/golang/glog"

	dputils "github.com/intel/sriov-network-device-plugin/pkg/utils"
	"github.com/openshift/sriov-network-operator/pkg/version"
)

type ddpInfo struct {
	FileName     string `json:"FileName,omitempty"`
	SourceUrl    string `json:"SourceUrl,omitempty"`
	BuildVersion string `json:"BuildVersion,omitempty"`
}

const (
	NoDdpState         = "No DDP loaded"
	ddpDirI40e         = "/lib/firmware/intel/i40e/ddp/"
	metaInfoFileSuffix = ".INFO"
)

var (
	ddpZipToProfileNameI40e = map[string]string{
		"esp-ah.zip":          "XL710 IP ESP, IP AH, UDP ESP",
		"gtp.zip":             "GTPv1-C/U IPv4/IPv6 payload",
		"ipv4-mcast.zip":      "IPv4 multicast PCTYPE",
		"l2tpv3oip-l4.zip":    "L2TPv3oIP with L4 payload",
		"mplsogreudp.zip":     "L2/L3 over MPLSoGRE/MPLSoUDP",
		"ppp-oe-ol2tpv2.zip":  "E710 PPPoE and PPPoL2TPv2",
		"radiofh4g.zip":       "Radio front haul 4G",
		"ecpri.zip":           "XL710 eCPRI",
	}
	ddpToolReturnCodes = map[int]string{
		0:  "Success",
		1:  "Bad command line parameter",
		2:  "Internal generic error",
		3:  "Insufficient privileges",
		4:  "No supported adapter",
		5:  "No base driver",
		6:  "Unsupported base driver",
		7:  "Cannot communicate with adapter",
		8:  "No DDP profile",
		9:  "Cannot read device data",
		10: "Cannot create output file",
		11: "Device not found",
	}
	ddpMetaInfo    = make(map[string]*ddpInfo)
)

// LoadDdp loads a DDP software which is extract from DDP package specified in SriovNetworkNodePolicy 'ddpUrl'
func LoadDdp(url, addr, pfname string) error {
	glog.Info("intel-plugin LoadDdp(): loading DDP onto NIC")

	var err error
	var fileName string
	var info *ddpInfo
	var stdout bytes.Buffer

	// Attempt to get DDP info
	info, err = getValidDdpInfo(url, ddpDirI40e, metaInfoFileSuffix)
	if err != nil {
		return err
	}

	// No valid DDP info found, therefore we fetch it
	if info == nil {
		var err error

		urlBase := filepath.Base(url)

		if fileName, err = fetchUnzipDdp(url, ddpDirI40e); err != nil {
			return err
		}
		info = &ddpInfo{}
		info.SourceUrl = url
		info.FileName = fileName
		info.BuildVersion = version.Raw
		ddpMetaInfo[urlBase + metaInfoFileSuffix] = info

		if err := updateDdpMetaInfo(ddpDirI40e, metaInfoFileSuffix); err != nil {
			return err
		}
		glog.Infof("intel-plugin LoadDdp(): added to meta data cache: '%v'", info)
	} else {
		fileName = info.FileName
	}

	if ok := ddpFileExists(fileName); ok != true {
		errMsg := fmt.Sprintf("intel-plugin LoadDdp(): file '%s' does not exist on host")
		glog.Errorf(errMsg)
		return fmt.Errorf(errMsg)
	}
	glog.Infof("intel-plugin LoadDdp(): loading DDP '%v' onto interface '%v'", fileName, pfname)

	cmd := exec.Command("ethtool", "-f", pfname, fileName, "100")
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		errMsg := fmt.Sprintf("intel-plugin LoadDdp(): error '%v' ethtool: '%v'", err, stdout.String())
		glog.Errorf(errMsg)
		return fmt.Errorf(errMsg)
	} else {
		glog.Info("intel-plugin LoadDdp(): successfully loaded DDP profile")
	}
	return nil
}

// UnloadDdp unloads a ddp profile
func UnloadDdp(pfname, addr string) error {
	cmd := exec.Command("ethtool", "-f", pfname, "-", "100")
	if err := cmd.Run(); err != nil {
		// When there are no DDP profiles, error will occur
		glog.Info("intel-plugin UnloadDdp(): no DDP is loaded")
	}

	return nil
}

// GetDdpVersion retrieves DDP version associated with PCI address argument 'dev'
func GetDdpVersion(dev string) (string, error) {
	var stdout bytes.Buffer
	ddpInfo := &dputils.DDPInfo{}

	cmd := exec.Command("ddptool", "-l", "-a", "-j", "-s", dev)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}

	if err := json.Unmarshal(stdout.Bytes(), ddpInfo); err != nil {
		return "", fmt.Errorf("intel-plugin GetDdpVersion(): failed to unmarshall with error: '%v'", err)
	}

	if ddpInfo.DDPInventory.DDPpackage.Version == "" {
		return "", fmt.Errorf("intel-plugin GetDdpVersion(): failed to retrieve DDP version")
	}

	return ddpInfo.DDPInventory.DDPpackage.Version, nil
}

// CheckDdpToolOutput will check the return code of DDP tool and log any issues
func CheckDdpToolOutput(pciAddr string, err interface{}) error {
	if exitError, ok := err.(*exec.ExitError); ok {
		exitCode := exitError.ExitCode()

		message, ok := ddpToolReturnCodes[exitCode]
		if !ok {
			return fmt.Errorf("intel-plugin CheckDdpToolOutput(): could not understand DDPTool return code: '%v'", exitCode)
		}

		switch exitCode {
		case 0:
			// Success
			return nil
		case 2:
			return fmt.Errorf("intel-plugin CheckDdpToolOutput(): an internal error has occurred. This maybe due " +
				"to unsupported device with PCI address '%s', DDPTool return message '%s' with error: '%v", pciAddr, message, err)
		case 4:
			return fmt.Errorf("intel-plugin CheckDdpToolOutput(): check Firmware/driver version for DDP support " +
				"for PCI address '%s', DDPTool return message '%s' with error '%v'", pciAddr, message, err)
		case 8:
			// Return Code 8 signifies no DDP loaded. This is not an error.
			return nil
		default:
			return fmt.Errorf("intel-plugin CheckDdpToolOutput(): error return code from DDPTool for PCI address " +
				"'%s', DDPTool return message '%s' with error '%v'", pciAddr, message, err)
		}
	} else {
		return fmt.Errorf("intel-plugin CheckDdpToolOutput(): failed to retrieve return code from DDPTool for " +
			"PCI address '%s' with error: '%v'", pciAddr, err)
	}
}

// HasDdpSupport using ddptool to test if a device supports DDP
func HasDdpSupport(pciAddr string) (bool, error) {
	if _, err := dputils.GetDDPProfiles(pciAddr); err != nil {
		if err := CheckDdpToolOutput(pciAddr, err); err != nil {
			return false, fmt.Errorf("intel-plugin HasDdpSupport(): Unable to detect DDP support with error: '%v'", err)
		}
		return true, nil
	}
	return true, nil
}

// SyncHostWContainer will copy DDP information from host to container. This will allow DDP software to persist.
func SyncHostWContainer() error {
	if _, err := os.Stat(ddpDirI40e); os.IsNotExist(err) {
		os.MkdirAll(ddpDirI40e, os.ModeDir)

	}
	if _, err := os.Stat("/host" + ddpDirI40e); os.IsNotExist(err) {
		os.MkdirAll("/host" + ddpDirI40e, os.ModeDir)
	}
	return copyDir("/host" + ddpDirI40e, ddpDirI40e)
}

// SyncContainerWHost will copy DDP information from container to host. This will allow DDP software to persist.
func SyncContainerWHost() error {
	if _, err := os.Stat(ddpDirI40e); os.IsNotExist(err) {
		os.MkdirAll(ddpDirI40e, os.ModeDir)
	}
	if _, err := os.Stat("/host" + ddpDirI40e); os.IsNotExist(err) {
		os.MkdirAll("/host" + ddpDirI40e, os.ModeDir)
	}
	return copyDir(ddpDirI40e, "/host" + ddpDirI40e)
}

// NeedsUpdate check whether we need to update - determine DDP profile from ddpUrl and compare to DDP profile from ddpStatus
func NeedsUpdate(ddpUrl, ddpState string) (bool, error) {
	if ddpUrl == "" && ddpState == NoDdpState {
		return false, nil
	}

	if ddpUrl == "" && ddpState != NoDdpState {
		return true, nil
	}

	ddpProfile, err := UrlToDdpProfile(ddpUrl)
	if err != nil {
		return false, err
	}

	if strings.Contains(ddpState, ddpProfile) {
		return false, nil
	}
	return true, nil
}

// UrlToDdpProfile convert url to DDP profile name
func UrlToDdpProfile(url string) (string, error) {
	if url == "" {
		return NoDdpState, nil
	}
	fileName := path.Base(url)
	ddpProfileName, err := fileToProfileNameI40e(fileName)
	if err != nil {
		errMsg := fmt.Sprintf("intel-plugin UrlToDdpProfile(): failed to convert filename to valid DDP profile with error: '%v'", err)
		glog.Error(errMsg)
		return NoDdpState, fmt.Errorf(errMsg)
	}
	return ddpProfileName, nil
}

// getDdpMetaInfo will gather DDP software info from the host
func getDdpMetaInfo(path, suffix string) error {
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return fmt.Errorf("intel-plugin getDdpMetaInfo(): unable to read directory '%s' contents with error: '%v'", path, err)
	}
	for _, file := range files {
		fileName := file.Name()
		if strings.HasSuffix(fileName, suffix) {
			var info ddpInfo

			jsonFile, err := os.Open(filepath.Join(path, fileName))
			if err != nil {
				return fmt.Errorf("intel-plugin getDdpMetaInfo(): unable to open file '%s' with error: '%v'", fileName, err)
			}

			fileBytes, err := ioutil.ReadAll(jsonFile)
			jsonFile.Close()
			if err != nil {
				return fmt.Errorf("intel-plugin getDdpMetaInfo(): unable to read file '%s' with error: '%v'", fileName, err)
			}

			err = json.Unmarshal([]byte(fileBytes), &info)
			if err != nil {
				return fmt.Errorf("intel-plugin getDdpMetaInfo(): unable to unmarshall file '%s' with error: '%v'", fileName, err)
			}
			ddpMetaInfo[fileName] = &info
		}
	}
	return nil
}

// ddpFileExists checks if profile is available on host
func ddpFileExists(profile string) bool {
	profilePath := filepath.Join(ddpDirI40e, profile)
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		return false
	}
	return true
}

// updateDdpMetaInfo will clear any existing meta info on disk and write new file containing DDP meta info
func updateDdpMetaInfo(path, suffix string) error {
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return fmt.Errorf("intel-plugin updateDdpMetaInfo(): failed to read files in directory '%s' with error: '%v'", path, err)
	}
	// Clean existing stale meta data cache files
	for _, file := range files {
		fileName := file.Name()
		if strings.HasSuffix(fileName, suffix) {
			err := os.Remove(filepath.Join(path, fileName))
			if err != nil {
				return fmt.Errorf("intel-plugin updateDdpMetaInfo(): failed to delete file '%s' with error: '%v'", fileName, err)
			}
		}
	}
	// Write meta info to file
	for key, value := range ddpMetaInfo {
		file, err := json.Marshal(*value)
		if err != nil {
			return fmt.Errorf("intel-plugin updateDdpMetaInfo(): failed to Marshall with error: '%v'", err)
		}
		if err = ioutil.WriteFile(filepath.Join(path, key), file, 0644); err != nil {
			return fmt.Errorf("intel-plugin updateDdpMetaInfo(): failed to write to JSON file with error: '%v'", err)
		}
		glog.Infof("intel-plugin updateDdpMetaInfo(): written info about profile to file: '%s'", key)
	}
	return nil
}

// getValidDdpInfo attempt to get DDP profile info. No info returned if not profile not found, source URL change or
// daemon build version change. This is to ensure we refresh the DDP profiles if there is a new build or change of source
func getValidDdpInfo(profileUrl, path, suffix string) (*ddpInfo, error) {
	fileBase := filepath.Base(profileUrl)

	if err := getDdpMetaInfo(path, suffix); err != nil {
		return nil, fmt.Errorf("intel-plugin getValidDdpInfo(): failed to refresh meta data cache with error: '%v'", err)
	}

	if val, found := ddpMetaInfo[fileBase + suffix]; found {
		// if URL changes or build version changes
		if strings.EqualFold(val.SourceUrl, profileUrl) && version.Raw == val.BuildVersion {
			return val, nil
		}
	}
	return nil, nil
}

// fetchUnzipDdp fetches a zip file from remote and stores in local directory dest. Returns DDP profile name found
func fetchUnzipDdp(url string, dest string) (string, error) {
	if !strings.HasSuffix(url, ".zip") {
		return "", fmt.Errorf("intel-plugin fetchUnzipDdp(): DDP URL does not seem to be a zip file: '%s'", url)
	}

	if _, err := os.Stat(dest); os.IsNotExist(err) {
		if err := os.MkdirAll(dest, 0644); err != nil {
			log.Fatal(err)
		}
	}

	res, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("intel-plugin fetchUnzipDdp(): unable to retrieve URL '%s' with error: '%v'", url, err)
	}

	if res.StatusCode != 200 {
		return "", fmt.Errorf("intel-plugin FetchDdPPrfileZip(): unable to fetch Zip from URL '%s' with return code '%v'", url, res.StatusCode)
	}
	defer res.Body.Close()

	baseName := path.Base(url)
	fullPath := path.Join(dest, baseName)
	out, err := os.Create(fullPath)
	if err != nil {
		return "", fmt.Errorf("intel-plugin FetchDdPPrfileZip(): unable to create file at path '%s' with error: '%v'", fullPath, err)
	}
	defer out.Close()
	defer os.Remove(fullPath)

	_, err = io.Copy(out, res.Body)
	if err != nil {
		return "", fmt.Errorf("intel-plugin FetchDdPPrfileZip(): unable to copy data to file at path '%s' with error: '%v'", fullPath, err)
	}

	ddpFileName, err := unzipDdpPkg(fullPath, dest)
	if err != nil {
		return "", err
	}
	return ddpFileName, nil
}

// unzipDdpPkg search through zip and only unzip DDP package to location destPath
func unzipDdpPkg(srcZipPath, destPath string) (string, error) {
	readCloser, err := zip.OpenReader(srcZipPath)
	if err != nil {
		return "", fmt.Errorf("intel-plugin unzipDdpPkg(): unable to open zip with error: '%v'", err)
	}
	defer readCloser.Close()

	for _, file := range readCloser.File {
		if strings.HasSuffix(file.Name, ".pkg") || strings.HasSuffix(file.Name, ".pkgo") {
			fullDestPath := path.Join(destPath, path.Base(file.Name))
			destF, err := os.OpenFile(fullDestPath, os.O_WRONLY | os.O_CREATE, file.Mode())
			if err != nil {
				return "", fmt.Errorf("intel-plugin unzipDdpPkg(): unable to open file with error: '%v'", err)
			}
			zipF, err := file.Open()
			if err != nil {
				return "", fmt.Errorf("intel-plugin unzipDdpPkg(): unable to open file with error: '%v'", err)
			}

			_, err = io.Copy(destF, zipF)

			destF.Close()
			zipF.Close()

			if err != nil {
				return "", fmt.Errorf("intel-plugin unzipDdpPkg(): unable to cpy data with error: '%v'", err)
			}

			return path.Base(file.Name), nil
		}
	}
	return "", fmt.Errorf("intel-plugin unzipDdpPkg(): unable to find DDP software from zip")
}

// copyDir copy contents of directory from source to destination
func copyDir(srcDir, destDir string) error {
	files, err := ioutil.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		sPath := filepath.Join(srcDir, file.Name())
		dPath := filepath.Join(destDir, file.Name())

		if err := copy(sPath, dPath); err != nil {
			return err
		}
	}
	return nil
}

// copy file from source to destination path
func copy(srcPath, destPath string) error {
	destFileR, err := os.Create(destPath)
	if err != nil {
		return nil
	}
	defer destFileR.Close()

	srcFileR, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFileR.Close()

	_, err = io.Copy(destFileR, srcFileR)
	if err != nil {
		return err
	}

	return nil
}

// fileToProfileNameI40e converts DDP package file name to DDP profile name
func fileToProfileNameI40e(ddpProfileFileName string) (string, error) {
	var filenames string
	if ddpName, ok := ddpZipToProfileNameI40e[ddpProfileFileName]; ok {
		return ddpName, nil
	}
	for fileName, _ := range ddpZipToProfileNameI40e {
		filenames = fmt.Sprintf("%s , %s", filenames, fileName)
	}
	return "", fmt.Errorf("intel-plugin fileToProfileNameI40e(): unable to convert DDP "+
		"filename '%s' to DDP profile name. Valid filenames are: '%s'", ddpProfileFileName, filenames[3:])
}