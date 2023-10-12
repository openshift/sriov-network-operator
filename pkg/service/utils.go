package service

import (
	"io"
	"os"
	"strings"

	"github.com/coreos/go-systemd/v22/unit"
	"github.com/golang/glog"
	"gopkg.in/yaml.v2"
)

const systemdDir = "/usr/lib/systemd/system/"

// CompareServices compare 2 service and return true if serviceA has all the fields of serviceB
func CompareServices(serviceA, serviceB *Service) (bool, error) {
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
		glog.Infof("DEBUG: %+v %v", optsA, *optB)
		return true, nil
	}

	return false, nil
}

// RemoveFromService removes given fields from service
func RemoveFromService(service *Service, options ...*unit.UnitOption) (*Service, error) {
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

// AppendToService appends given fields to service
func AppendToService(service *Service, options ...*unit.UnitOption) (*Service, error) {
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

// ReadServiceInjectionManifestFile reads service injection file
func ReadServiceInjectionManifestFile(path string) (*Service, error) {
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
func ReadServiceManifestFile(path string) (*Service, error) {
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
func ReadScriptManifestFile(path string) (*ScriptManifestFile, error) {
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
