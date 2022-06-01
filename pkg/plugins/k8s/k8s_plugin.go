package k8s

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/coreos/go-systemd/v22/unit"
	"github.com/golang/glog"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	plugins "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/service"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

var PluginName = "k8s_plugin"

type K8sPlugin struct {
	PluginName                 string
	SpecVersion                string
	serviceManager             service.ServiceManager
	switchdevBeforeNMRunScript *service.ScriptManifestFile
	switchdevAfterNMRunScript  *service.ScriptManifestFile
	switchdevUdevScript        *service.ScriptManifestFile
	switchdevBeforeNMService   *service.Service
	switchdevAfterNMService    *service.Service
	openVSwitchService         *service.Service
	networkManagerService      *service.Service
	updateTarget               *k8sUpdateTarget
}

type k8sUpdateTarget struct {
	switchdevBeforeNMService   bool
	switchdevAfterNMService    bool
	switchdevBeforeNMRunScript bool
	switchdevAfterNMRunScript  bool
	switchdevUdevScript        bool
	systemServices             []*service.Service
}

func (u *k8sUpdateTarget) needUpdate() bool {
	return u.switchdevBeforeNMService || u.switchdevAfterNMService || u.switchdevBeforeNMRunScript || u.switchdevAfterNMRunScript || u.switchdevUdevScript || len(u.systemServices) > 0
}

func (u *k8sUpdateTarget) needReboot() bool {
	return u.switchdevBeforeNMService || u.switchdevAfterNMService || u.switchdevBeforeNMRunScript || u.switchdevAfterNMRunScript || u.switchdevUdevScript
}

func (u *k8sUpdateTarget) reset() {
	u.switchdevBeforeNMService = false
	u.switchdevAfterNMService = false
	u.switchdevBeforeNMRunScript = false
	u.switchdevAfterNMRunScript = false
	u.systemServices = []*service.Service{}
}

func (u *k8sUpdateTarget) String() string {
	var updateList []string
	if u.switchdevBeforeNMService || u.switchdevAfterNMService {
		updateList = append(updateList, "SwitchdevService")
	}
	if u.switchdevBeforeNMRunScript || u.switchdevAfterNMRunScript {
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
	switchdevManifestPath             = "bindata/manifests/switchdev-config/"
	switchdevUnits                    = switchdevManifestPath + "switchdev-units/"
	switchdevBeforeNMUnitFile         = switchdevUnits + "switchdev-configuration-before-nm.yaml"
	switchdevAfterNMUnitFile          = switchdevUnits + "switchdev-configuration-after-nm.yaml"
	networkManagerUnitFile            = switchdevUnits + "NetworkManager.service.yaml"
	ovsUnitFile                       = switchdevManifestPath + "ovs-units/ovs-vswitchd.service.yaml"
	configuresSwitchdevBeforeNMScript = switchdevManifestPath + "files/switchdev-configuration-before-nm.sh.yaml"
	configuresSwitchdevAfterNMScript  = switchdevManifestPath + "files/switchdev-configuration-after-nm.sh.yaml"
	switchdevRenamingUdevScript       = switchdevManifestPath + "files/switchdev-vf-link-name.sh.yaml"

	chroot = "/host"
)

// Initialize our plugin and set up initial values
func NewK8sPlugin() (plugins.VendorPlugin, error) {
	k8sPluging := &K8sPlugin{
		PluginName:     PluginName,
		SpecVersion:    "1.0",
		serviceManager: service.NewServiceManager(chroot),
		updateTarget:   &k8sUpdateTarget{},
	}

	return k8sPluging, k8sPluging.readManifestFiles()
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
	// Update services if switchdev required
	if !utils.IsSwitchdevModeSpec(new.Spec) {
		return
	}

	// Check services
	err = p.servicesStateUpdate()
	if err != nil {
		glog.Errorf("k8s-plugin OnNodeStateChange(): failed : %v", err)
		return
	}

	if p.updateTarget.needUpdate() {
		needDrain = true
		if p.updateTarget.needReboot() {
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
	if err := p.updateSwitchdevService(); err != nil {
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
	switchdevBeforeNMService, err := service.ReadServiceManifestFile(switchdevBeforeNMUnitFile)
	if err != nil {
		return err
	}
	switchdevAfterNMService, err := service.ReadServiceManifestFile(switchdevAfterNMUnitFile)
	if err != nil {
		return err
	}

	// Remove run condition form the service
	conditionOpt := &unit.UnitOption{
		Section: "Unit",
		Name:    "ConditionPathExists",
		Value:   "!/etc/ignition-machine-config-encapsulated.json",
	}
	switchdevBeforeNMService, err = service.RemoveFromService(switchdevBeforeNMService, conditionOpt)
	if err != nil {
		return err
	}
	switchdevAfterNMService, err = service.RemoveFromService(switchdevAfterNMService, conditionOpt)
	if err != nil {
		return err
	}
	p.switchdevBeforeNMService = switchdevBeforeNMService
	p.switchdevAfterNMService = switchdevAfterNMService

	// Read switchdev run script
	switchdevBeforeNMRunScript, err := service.ReadScriptManifestFile(configuresSwitchdevBeforeNMScript)
	if err != nil {
		return err
	}
	switchdevAfterNMRunScript, err := service.ReadScriptManifestFile(configuresSwitchdevAfterNMScript)
	if err != nil {
		return err
	}
	p.switchdevBeforeNMRunScript = switchdevBeforeNMRunScript
	p.switchdevAfterNMRunScript = switchdevAfterNMRunScript

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
	needUpdate, err := p.isSwitchdevServiceNeedUpdate(p.switchdevBeforeNMService)
	if err != nil {
		return err
	}
	p.updateTarget.switchdevBeforeNMService = needUpdate
	needUpdate, err = p.isSwitchdevServiceNeedUpdate(p.switchdevAfterNMService)
	if err != nil {
		return err
	}
	p.updateTarget.switchdevAfterNMService = needUpdate

	// Check switchdev run script
	needUpdate, err = p.isSwitchdevScriptNeedUpdate(p.switchdevBeforeNMRunScript)
	if err != nil {
		return err
	}
	p.updateTarget.switchdevBeforeNMRunScript = needUpdate
	needUpdate, err = p.isSwitchdevScriptNeedUpdate(p.switchdevAfterNMRunScript)
	if err != nil {
		return err
	}
	p.updateTarget.switchdevAfterNMRunScript = needUpdate

	// Check switchdev udev script
	needUpdate, err = p.isSwitchdevScriptNeedUpdate(p.switchdevUdevScript)
	if err != nil {
		return err
	}
	p.updateTarget.switchdevUdevScript = needUpdate

	return nil
}

func (p *K8sPlugin) getSystemServices() []*service.Service {
	return []*service.Service{p.networkManagerService, p.openVSwitchService}
}

func (p *K8sPlugin) isSwitchdevScriptNeedUpdate(scriptObj *service.ScriptManifestFile) (needUpdate bool, err error) {
	data, err := ioutil.ReadFile(path.Join(chroot, scriptObj.Path))
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		return true, nil
	} else if string(data) != scriptObj.Contents.Inline {
		return true, nil
	}
	return false, nil
}

func (p *K8sPlugin) isSwitchdevServiceNeedUpdate(serviceObj *service.Service) (needUpdate bool, err error) {
	swdService, err := p.serviceManager.ReadService(serviceObj.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		// service not exists
		return true, nil
	} else {
		needChange, err := service.CompareServices(swdService, serviceObj)
		if err != nil {
			return false, err
		}
		return needChange, nil
	}
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

func (p *K8sPlugin) updateSwitchdevService() error {
	if p.updateTarget.switchdevBeforeNMService {
		err := p.serviceManager.EnableService(p.switchdevBeforeNMService)
		if err != nil {
			return err
		}
	}

	if p.updateTarget.switchdevAfterNMService {
		err := p.serviceManager.EnableService(p.switchdevAfterNMService)
		if err != nil {
			return err
		}
	}

	if p.updateTarget.switchdevBeforeNMRunScript {
		err := ioutil.WriteFile(path.Join(chroot, p.switchdevBeforeNMRunScript.Path),
			[]byte(p.switchdevBeforeNMRunScript.Contents.Inline), 0755)
		if err != nil {
			return err
		}
	}

	if p.updateTarget.switchdevAfterNMRunScript {
		err := ioutil.WriteFile(path.Join(chroot, p.switchdevAfterNMRunScript.Path),
			[]byte(p.switchdevAfterNMRunScript.Contents.Inline), 0755)
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
