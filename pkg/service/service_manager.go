package service

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

type ServiceManager interface {
	IsServiceExist(string) (bool, error)
	ReadService(string) (*Service, error)
	EnableService(service *Service) error
}

type serviceManager struct {
	chroot string
}

func NewServiceManager(chroot string) ServiceManager {
	root := chroot
	if root == "" {
		root = "/"
	}
	return &serviceManager{root}
}

// ReadService read service from given path
func (sm *serviceManager) IsServiceExist(servicePath string) (bool, error) {
	_, err := os.Stat(path.Join(sm.chroot, servicePath))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// ReadService read service from given path
func (sm *serviceManager) ReadService(servicePath string) (*Service, error) {
	data, err := os.ReadFile(path.Join(sm.chroot, servicePath))
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
func (sm *serviceManager) EnableService(service *Service) error {
	// Write service file
	err := os.WriteFile(path.Join(sm.chroot, service.Path), []byte(service.Content), 0644)
	if err != nil {
		return err
	}

	// Change root dir
	exit, err := utils.Chroot(sm.chroot)
	if err != nil {
		return err
	}
	defer exit()

	// Enable service
	cmd := exec.Command("systemctl", "enable", service.Name)
	return cmd.Run()
}
