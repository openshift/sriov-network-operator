package service

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
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

// TODO: handle this to support unit-tests
const systemdDir = "/usr/lib/systemd/system/"

type service struct {
	utilsHelper utils.CmdInterface
}

func New(utilsHelper utils.CmdInterface) types.ServiceInterface {
	return &service{utilsHelper: utilsHelper}
}

// ServiceInjectionManifestFile service injection manifest file structure
type ServiceInjectionManifestFile struct {
	Name    string
	Dropins []struct {
		Contents string
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
func (s *service) ReadService(servicePath string) (*types.Service, error) {
	data, err := os.ReadFile(path.Join(consts.Chroot, servicePath))
	if err != nil {
		return nil, err
	}

	return &types.Service{
		Name:    filepath.Base(servicePath),
		Path:    servicePath,
		Content: string(data),
	}, nil
}

// EnableService creates service file and enables it with systemctl enable
func (s *service) EnableService(service *types.Service) error {
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

	// Enable the service
	// we use reenable command (the command is a combination of disable+enable) to reset
	// symlinks for the unit and make sure that only symlinks that are currently
	// configured in the [Install] section exist for the service.
	_, _, err = s.utilsHelper.RunCommand("systemctl", "reenable", service.Name)
	return err
}

// CompareServices returns true if serviceA needs update(doesn't contain all fields from service B)
func (s *service) CompareServices(serviceA, serviceB *types.Service) (bool, error) {
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

// ReadServiceInjectionManifestFile reads service injection file
func (s *service) ReadServiceInjectionManifestFile(path string) (*types.Service, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var serviceContent ServiceInjectionManifestFile
	if err := yaml.Unmarshal(data, &serviceContent); err != nil {
		return nil, err
	}

	return &types.Service{
		Name:    serviceContent.Name,
		Path:    systemdDir + serviceContent.Name,
		Content: serviceContent.Dropins[0].Contents,
	}, nil
}

// ReadServiceManifestFile reads service file
func (s *service) ReadServiceManifestFile(path string) (*types.Service, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var serviceFile *types.ServiceManifestFile
	if err := yaml.Unmarshal(data, &serviceFile); err != nil {
		return nil, err
	}

	return &types.Service{
		Name:    serviceFile.Name,
		Path:    "/etc/systemd/system/" + serviceFile.Name,
		Content: serviceFile.Contents,
	}, nil
}

func (s *service) UpdateSystemService(serviceObj *types.Service) error {
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
func appendToService(service *types.Service, options ...*unit.UnitOption) (*types.Service, error) {
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

	return &types.Service{
		Name:    service.Name,
		Path:    service.Path,
		Content: string(data),
	}, nil
}
