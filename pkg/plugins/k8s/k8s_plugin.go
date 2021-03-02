package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/coreos/go-systemd/v22/unit"
	"github.com/golang/glog"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/service"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

type K8sPlugin struct {
	PluginName            string
	SpecVersion           string
	serviceManager        service.ServiceManager
	switchdevRunScript    *service.ScriptManifestFile
	switchdevUdevScript   *service.ScriptManifestFile
	switchdevService      *service.Service
	openVSwitchService    *service.Service
	networkManagerService *service.Service
	updateTarget          *k8sUpdateTarget
}

type k8sUpdateTarget struct {
	switchdevService    bool
	switchdevRunScript  bool
	switchdevUdevScript bool
	systemServices      []*service.Service
}

func (u *k8sUpdateTarget) needUpdate() bool {
	return u.switchdevService || u.switchdevRunScript || u.switchdevUdevScript || len(u.systemServices) > 0
}

func (u *k8sUpdateTarget) reset() {
	u.switchdevService = false
	u.switchdevRunScript = false
	u.systemServices = []*service.Service{}
}

func (u *k8sUpdateTarget) String() string {
	var updateList []string
	if u.switchdevService {
		updateList = append(updateList, "SwitchdevService")
	}
	if u.switchdevRunScript {
		updateList = append(updateList, "SwitchdevRunScript")
	}
	if u.switchdevUdevScript {
		updateList = append(updateList, "SwitchdevUdevScript")
	}
	for _, s := range u.systemServices {
		updateList = append(updateList, s.Name)
	}

	return strings.Join(updateList, ",")
}

const (
	switchdevManifestPath       = "bindata/manifests/switchdev-config/"
	switchdevUnits              = switchdevManifestPath + "switchdev-units/"
	switchdevUnitFile           = switchdevUnits + "switchdev-configuration.yaml"
	networkManagerUnitFile      = switchdevUnits + "NetworkManager.service.yaml"
	ovsUnitFile                 = switchdevManifestPath + "ovs-units/ovs-vswitchd.service.yaml"
	configuresSwitchdevScript   = switchdevManifestPath + "files/configure-switchdev.sh.yaml"
	switchdevRenamingUdevScript = switchdevManifestPath + "files/switchdev-vf-link-name.sh.yaml"

	chroot = "/host"
)

var (
	Plugin K8sPlugin
)

// Initialize our plugin and set up initial values
func init() {
	Plugin = K8sPlugin{
		PluginName:     "k8s_plugin",
		SpecVersion:    "1.0",
		serviceManager: service.NewServiceManager(chroot),
		updateTarget:   &k8sUpdateTarget{},
	}

	// Read manifest files for plugin
	if err := Plugin.readManifestFiles(); err != nil {
		panic(err)
	}
}

// Name returns the name of the plugin
func (p *K8sPlugin) Name() string {
	return p.PluginName
}

// Spec returns the version of the spec expected by the plugin
func (p *K8sPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateAdd Invoked when SriovNetworkNodeState CR is created, return if need dain and/or reboot node
func (p *K8sPlugin) OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("k8s-plugin OnNodeStateAdd()")
	return p.OnNodeStateChange(nil, state)
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
func (p *K8sPlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("k8s-plugin OnNodeStateChange()")
	needDrain = false
	needReboot = false

	p.updateTarget.reset()
	// TODO add check for enableOvsOffload in OperatorConfig later
	// Update services if switchdev not required
	if utils.IsSwitchdevModeSpec(new.Spec) {
		// Check services
		err = p.servicesStateUpdate()
		if err != nil {
			glog.Errorf("k8s-plugin OnNodeStateChange(): failed : %v", err)
			return
		}
	}

	// Check switchdev config
	var update, remove bool
	if update, remove, err = utils.WriteSwitchdevConfFile(new); err != nil {
		glog.Errorf("k8s-plugin OnNodeStateChange():fail to update switchdev.conf file: %v", err)
		return
	}
	if remove {
		glog.Info("k8s-plugin OnNodeStateChange(): need reboot node to clean switchdev VFs")
		needDrain = true
		needReboot = true
		return
	}
	if update {
		glog.Info("k8s-plugin OnNodeStateChange(): need reboot node to use the up-to-date switchdev.conf")
		needDrain = true
		needReboot = true
		return
	}
	if p.updateTarget.needUpdate() {
		needDrain = true
		if p.updateTarget.switchdevUdevScript {
			needReboot = true
			glog.Infof("k8s-plugin OnNodeStateChange(): needReboot to update %q", p.updateTarget)
		} else {
			glog.Infof("k8s-plugin OnNodeStateChange(): needDrain to update %q", p.updateTarget)
		}
	}

	return
}

// Apply config change
func (p *K8sPlugin) Apply() error {
	glog.Info("k8s-plugin Apply()")
	if err := p.updateSwichdevService(); err != nil {
		return err
	}

	for _, systemService := range p.updateTarget.systemServices {
		if err := p.updateSystemService(systemService); err != nil {
			return err
		}
	}

	return nil
}

func (p *K8sPlugin) readSwitchdevManifest() error {
	// Read switchdev service
	switchdevService, err := service.ReadServiceManifestFile(switchdevUnitFile)
	if err != nil {
		return err
	}

	// Remove run condition form the service
	conditionOpt := &unit.UnitOption{
		Section: "Unit",
		Name:    "ConditionPathExists",
		Value:   "!/etc/ignition-machine-config-encapsulated.json",
	}
	switchdevService, err = service.RemoveFromService(switchdevService, conditionOpt)
	if err != nil {
		return err
	}
	p.switchdevService = switchdevService

	// Read switchdev run script
	switchdevRunScript, err := service.ReadScriptManifestFile(configuresSwitchdevScript)
	if err != nil {
		return err
	}
	p.switchdevRunScript = switchdevRunScript

	// Read switchdev udev script
	switchdevUdevScript, err := service.ReadScriptManifestFile(switchdevRenamingUdevScript)
	if err != nil {
		return err
	}
	p.switchdevUdevScript = switchdevUdevScript

	return nil
}

func (p *K8sPlugin) readNetworkManagerManifest() error {
	networkManagerService, err := service.ReadServiceInjectionManifestFile(networkManagerUnitFile)
	if err != nil {
		return err
	}

	p.networkManagerService = networkManagerService
	return nil
}

func (p *K8sPlugin) readOpenVSwitchdManifest() error {
	openVSwitchService, err := service.ReadServiceInjectionManifestFile(ovsUnitFile)
	if err != nil {
		return err
	}

	p.openVSwitchService = openVSwitchService
	return nil
}

func (p *K8sPlugin) readManifestFiles() error {
	if err := p.readSwitchdevManifest(); err != nil {
		return err
	}

	if err := p.readNetworkManagerManifest(); err != nil {
		return err
	}

	if err := p.readOpenVSwitchdManifest(); err != nil {
		return err
	}

	return nil
}

func (p *K8sPlugin) switchdevServiceStateUpdate() error {
	// Check switchdev service
	swdService, err := p.serviceManager.ReadService(p.switchdevService.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// service not exists
		p.updateTarget.switchdevService = true
	} else {
		needChange, err := service.CompareServices(swdService, p.switchdevService)
		if err != nil {
			return err
		}
		p.updateTarget.switchdevService = needChange
	}

	// Check switchdev run script
	data, err := ioutil.ReadFile(path.Join(chroot, p.switchdevRunScript.Path))
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		p.updateTarget.switchdevRunScript = true
	} else if string(data) != p.switchdevRunScript.Contents.Inline {
		p.updateTarget.switchdevRunScript = true
	}

	// Check switchdev udev script
	data, err = ioutil.ReadFile(path.Join(chroot, p.switchdevUdevScript.Path))
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		p.updateTarget.switchdevUdevScript = true
	} else if string(data) != p.switchdevUdevScript.Contents.Inline {
		p.updateTarget.switchdevUdevScript = true
	}

	return nil
}

func (p *K8sPlugin) getSystemServices() []*service.Service {
	return []*service.Service{p.networkManagerService, p.openVSwitchService}
}

func (p *K8sPlugin) isSystemServiceNeedUpdate(serviceObj *service.Service) bool {
	systemService, err := p.serviceManager.ReadService(serviceObj.Path)
	if err != nil {
		glog.Warningf("k8s-plugin isSystemServiceNeedUpdate(): failed to read switchdev service file %q: %v",
			serviceObj.Path, err)
		return false
	}
	if systemService != nil {
		needChange, err := service.CompareServices(systemService, serviceObj)
		if err != nil {
			glog.Warningf("k8s-plugin isSystemServiceNeedUpdate(): failed to compare switchdev service : %v", err)
			return false
		}
		return needChange
	}

	return false
}

func (p *K8sPlugin) systemServicesStateUpdate() error {
	var services []*service.Service
	for _, systemService := range p.getSystemServices() {
		exist, err := p.serviceManager.IsServiceExist(systemService.Path)
		if err != nil {
			return err
		}
		if !exist {
			return fmt.Errorf("k8s-plugin systemServicesStateUpdate(): %q not found", systemService.Name)
		}
		if p.isSystemServiceNeedUpdate(systemService) {
			services = append(services, systemService)
		}
	}

	p.updateTarget.systemServices = services
	return nil
}

func (p *K8sPlugin) servicesStateUpdate() error {
	// Check switchdev
	err := p.switchdevServiceStateUpdate()
	if err != nil {
		return err
	}

	// Check system services
	err = p.systemServicesStateUpdate()
	if err != nil {
		return err
	}

	return nil
}

func (p *K8sPlugin) updateSwichdevService() error {
	if p.updateTarget.switchdevService {
		err := p.serviceManager.EnableService(p.switchdevService)
		if err != nil {
			return err
		}
	}

	if p.updateTarget.switchdevRunScript {
		err := ioutil.WriteFile(path.Join(chroot, p.switchdevRunScript.Path),
			[]byte(p.switchdevRunScript.Contents.Inline), 0755)
		if err != nil {
			return err
		}
	}

	if p.updateTarget.switchdevUdevScript {
		err := ioutil.WriteFile(path.Join(chroot, p.switchdevUdevScript.Path),
			[]byte(p.switchdevUdevScript.Contents.Inline), 0755)
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *K8sPlugin) updateSystemService(serviceObj *service.Service) error {
	systemService, err := p.serviceManager.ReadService(serviceObj.Path)
	if err != nil {
		return err
	}
	if systemService == nil {
		// Invalid case to reach here
		return fmt.Errorf("k8s-plugin Apply(): can't update non-existing service %q", serviceObj.Name)
	}
	serviceOptions, err := unit.Deserialize(strings.NewReader(serviceObj.Content))
	if err != nil {
		return err
	}
	updatedService, err := service.AppendToService(systemService, serviceOptions...)
	if err != nil {
		return err
	}

	return p.serviceManager.EnableService(updatedService)
}
