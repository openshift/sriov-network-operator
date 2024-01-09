package host

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/coreos/go-systemd/v22/unit"
	"gopkg.in/yaml.v3"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

// TODO: handle this to support unit-tests
const systemdDir = "/usr/lib/systemd/system/"

var (
	// Remove run condition form the service
	ConditionOpt = &unit.UnitOption{
		Section: "Unit",
		Name:    "ConditionPathExists",
		Value:   "!/etc/ignition-machine-config-encapsulated.json",
	}
)

type ServiceInterface interface {
	// IsServiceExist checks if the requested systemd service exist on the system
	IsServiceExist(string) (bool, error)
	// IsServiceEnabled checks if the requested systemd service is enabled on the system
	IsServiceEnabled(string) (bool, error)
	// ReadService reads a systemd servers and return it as a struct
	ReadService(string) (*Service, error)
	// EnableService enables a systemd server on the host
	EnableService(service *Service) error
	// ReadServiceManifestFile reads the systemd manifest for a specific service
	ReadServiceManifestFile(path string) (*Service, error)
	// RemoveFromService removes a systemd service from the host
	RemoveFromService(service *Service, options ...*unit.UnitOption) (*Service, error)
	// ReadScriptManifestFile reads the script manifest from a systemd service
	ReadScriptManifestFile(path string) (*ScriptManifestFile, error)
	// ReadServiceInjectionManifestFile reads the injection manifest file for the systemd service
	ReadServiceInjectionManifestFile(path string) (*Service, error)
	// CompareServices compare two servers and return true if they are equal
	CompareServices(serviceA, serviceB *Service) (bool, error)
	// UpdateSystemService updates a system service on the host
	UpdateSystemService(serviceObj *Service) error
}

type service struct {
	utilsHelper utils.CmdInterface
}

func newServiceInterface(utilsHelper utils.CmdInterface) ServiceInterface {
	return &service{utilsHelper: utilsHelper}
}

type Service struct {
	Name    string
	Path    string
	Content string
}

// ServiceInjectionManifestFile service injection manifest file structure
type ServiceInjectionManifestFile struct {
	Name    string
	Dropins []struct {
		Contents string
	}
}

// ServiceManifestFile service manifest file structure
type ServiceManifestFile struct {
	Name     string
	Contents string
}

// ScriptManifestFile script manifest file structure
type ScriptManifestFile struct {
	Path     string
	Contents struct {
		Inline string
	}
}

// IsServiceExist check if service unit exist
func (s *service) IsServiceExist(servicePath string) (bool, error) {
	_, err := os.Stat(path.Join(consts.Chroot, servicePath))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// IsServiceEnabled check if service exist and enabled
func (s *service) IsServiceEnabled(servicePath string) (bool, error) {
	exist, err := s.IsServiceExist(servicePath)
	if err != nil || !exist {
		return false, err
	}
	serviceName := filepath.Base(servicePath)
	// Change root dir
	exit, err := s.utilsHelper.Chroot(consts.Chroot)
	if err != nil {
		return false, err
	}
	defer exit()

	// TODO: add check for the output and logs
	_, _, err = s.utilsHelper.RunCommand("systemctl", "is-enabled", serviceName)
	return err == nil, nil
}

// ReadService read service from given path
func (s *service) ReadService(servicePath string) (*Service, error) {
	data, err := os.ReadFile(path.Join(consts.Chroot, servicePath))
	if err != nil {
		return nil, err
	}

	return &Service{
		Name:    filepath.Base(servicePath),
		Path:    servicePath,
		Content: string(data),
	}, nil
}

// EnableService creates service file and enables it with systemctl enable
func (s *service) EnableService(service *Service) error {
	// Write service file
	err := os.WriteFile(path.Join(consts.Chroot, service.Path), []byte(service.Content), 0644)
	if err != nil {
		return err
	}

	// Change root dir
	exit, err := s.utilsHelper.Chroot(consts.Chroot)
	if err != nil {
		return err
	}
	defer exit()

	// Enable service
	_, _, err = s.utilsHelper.RunCommand("systemctl", "enable", service.Name)
	return err
}

// CompareServices compare 2 service and return true if serviceA has all the fields of serviceB
func (s *service) CompareServices(serviceA, serviceB *Service) (bool, error) {
	optsA, err := unit.Deserialize(strings.NewReader(serviceA.Content))
	if err != nil {
		return false, err
	}
	optsB, err := unit.Deserialize(strings.NewReader(serviceB.Content))
	if err != nil {
		return false, err
	}

OUTER:
	for _, optB := range optsB {
		for _, optA := range optsA {
			if optA.Match(optB) {
				continue OUTER
			}
		}
		log.Log.V(2).Info("CompareServices", "ServiceA", optsA, "ServiceB", *optB)
		return true, nil
	}

	return false, nil
}

// RemoveFromService removes given fields from service
func (s *service) RemoveFromService(service *Service, options ...*unit.UnitOption) (*Service, error) {
	opts, err := unit.Deserialize(strings.NewReader(service.Content))
	if err != nil {
		return nil, err
	}

	var newServiceOptions []*unit.UnitOption
OUTER:
	for _, opt := range opts {
		for _, optRemove := range options {
			if opt.Match(optRemove) {
				continue OUTER
			}
		}

		newServiceOptions = append(newServiceOptions, opt)
	}

	data, err := io.ReadAll(unit.Serialize(newServiceOptions))
	if err != nil {
		return nil, err
	}

	return &Service{
		Name:    service.Name,
		Path:    service.Path,
		Content: string(data),
	}, nil
}

// ReadServiceInjectionManifestFile reads service injection file
func (s *service) ReadServiceInjectionManifestFile(path string) (*Service, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var serviceContent ServiceInjectionManifestFile
	if err := yaml.Unmarshal(data, &serviceContent); err != nil {
		return nil, err
	}

	return &Service{
		Name:    serviceContent.Name,
		Path:    systemdDir + serviceContent.Name,
		Content: serviceContent.Dropins[0].Contents,
	}, nil
}

// ReadServiceManifestFile reads service file
func (s *service) ReadServiceManifestFile(path string) (*Service, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var serviceFile *ServiceManifestFile
	if err := yaml.Unmarshal(data, &serviceFile); err != nil {
		return nil, err
	}

	return &Service{
		Name:    serviceFile.Name,
		Path:    "/etc/systemd/system/" + serviceFile.Name,
		Content: serviceFile.Contents,
	}, nil
}

// ReadScriptManifestFile reads script file
func (s *service) ReadScriptManifestFile(path string) (*ScriptManifestFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var scriptFile *ScriptManifestFile
	if err := yaml.Unmarshal(data, &scriptFile); err != nil {
		return nil, err
	}

	return scriptFile, nil
}

func (s *service) UpdateSystemService(serviceObj *Service) error {
	systemService, err := s.ReadService(serviceObj.Path)
	if err != nil {
		return err
	}
	if systemService == nil {
		// Invalid case to reach here
		return fmt.Errorf("can't update non-existing service %q", serviceObj.Name)
	}
	serviceOptions, err := unit.Deserialize(strings.NewReader(serviceObj.Content))
	if err != nil {
		return err
	}
	updatedService, err := appendToService(systemService, serviceOptions...)
	if err != nil {
		return err
	}

	return s.EnableService(updatedService)
}

// appendToService appends given fields to service
func appendToService(service *Service, options ...*unit.UnitOption) (*Service, error) {
	serviceOptions, err := unit.Deserialize(strings.NewReader(service.Content))
	if err != nil {
		return nil, err
	}

OUTER:
	for _, appendOpt := range options {
		for _, opt := range serviceOptions {
			if opt.Match(appendOpt) {
				continue OUTER
			}
		}
		serviceOptions = append(serviceOptions, appendOpt)
	}

	data, err := io.ReadAll(unit.Serialize(serviceOptions))
	if err != nil {
		return nil, err
	}

	return &Service{
		Name:    service.Name,
		Path:    service.Path,
		Content: string(data),
	}, nil
}
