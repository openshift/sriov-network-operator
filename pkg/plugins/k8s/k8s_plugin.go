package k8s

import (
	"fmt"
	"os"
	"path"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host"
	plugins "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var PluginName = "k8s_plugin"

type K8sPlugin struct {
	PluginName                 string
	SpecVersion                string
	switchdevBeforeNMRunScript *host.ScriptManifestFile
	switchdevAfterNMRunScript  *host.ScriptManifestFile
	switchdevUdevScript        *host.ScriptManifestFile
	switchdevBeforeNMService   *host.Service
	switchdevAfterNMService    *host.Service
	openVSwitchService         *host.Service
	networkManagerService      *host.Service
	sriovService               *host.Service
	updateTarget               *k8sUpdateTarget
	hostHelper                 helper.HostHelpersInterface
}

type k8sUpdateTarget struct {
	switchdevBeforeNMService   bool
	switchdevAfterNMService    bool
	switchdevBeforeNMRunScript bool
	switchdevAfterNMRunScript  bool
	switchdevUdevScript        bool
	sriovScript                bool
	systemServices             []*host.Service
}

func (u *k8sUpdateTarget) needUpdate() bool {
	return u.switchdevBeforeNMService || u.switchdevAfterNMService || u.switchdevBeforeNMRunScript || u.switchdevAfterNMRunScript || u.switchdevUdevScript || u.sriovScript || len(u.systemServices) > 0
}

func (u *k8sUpdateTarget) needReboot() bool {
	return u.switchdevBeforeNMService || u.switchdevAfterNMService || u.switchdevBeforeNMRunScript || u.switchdevAfterNMRunScript || u.switchdevUdevScript || u.sriovScript
}

func (u *k8sUpdateTarget) reset() {
	u.switchdevBeforeNMService = false
	u.switchdevAfterNMService = false
	u.switchdevBeforeNMRunScript = false
	u.switchdevAfterNMRunScript = false
	u.switchdevUdevScript = false
	u.sriovScript = false
	u.systemServices = []*host.Service{}
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
	bindataManifestPath               = "bindata/manifests/"
	switchdevManifestPath             = bindataManifestPath + "switchdev-config/"
	switchdevUnits                    = switchdevManifestPath + "switchdev-units/"
	sriovUnits                        = bindataManifestPath + "sriov-config-service/kubernetes/"
	sriovUnitFile                     = sriovUnits + "sriov-config-service.yaml"
	switchdevBeforeNMUnitFile         = switchdevUnits + "switchdev-configuration-before-nm.yaml"
	switchdevAfterNMUnitFile          = switchdevUnits + "switchdev-configuration-after-nm.yaml"
	networkManagerUnitFile            = switchdevUnits + "NetworkManager.service.yaml"
	ovsUnitFile                       = switchdevManifestPath + "ovs-units/ovs-vswitchd.service.yaml"
	configuresSwitchdevBeforeNMScript = switchdevManifestPath + "files/switchdev-configuration-before-nm.sh.yaml"
	configuresSwitchdevAfterNMScript  = switchdevManifestPath + "files/switchdev-configuration-after-nm.sh.yaml"
	switchdevRenamingUdevScript       = switchdevManifestPath + "files/switchdev-vf-link-name.sh.yaml"
)

// Initialize our plugin and set up initial values
func NewK8sPlugin(helper helper.HostHelpersInterface) (plugins.VendorPlugin, error) {
	k8sPluging := &K8sPlugin{
		PluginName:   PluginName,
		SpecVersion:  "1.0",
		hostHelper:   helper,
		updateTarget: &k8sUpdateTarget{},
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

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is created or updated, return if need dain and/or reboot node
func (p *K8sPlugin) OnNodeStateChange(new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	log.Log.Info("k8s-plugin OnNodeStateChange()")
	needDrain = false
	needReboot = false

	p.updateTarget.reset()
	// TODO add check for enableOvsOffload in OperatorConfig later
	// Update services if switchdev required
	if !vars.UsingSystemdMode && !sriovnetworkv1.IsSwitchdevModeSpec(new.Spec) {
		return
	}

	if sriovnetworkv1.IsSwitchdevModeSpec(new.Spec) {
		// Check services
		err = p.switchDevServicesStateUpdate()
		if err != nil {
			log.Log.Error(err, "k8s-plugin OnNodeStateChange(): failed")
			return
		}
	}

	if vars.UsingSystemdMode {
		// Check sriov service
		err = p.sriovServiceStateUpdate()
		if err != nil {
			log.Log.Error(err, "k8s-plugin OnNodeStateChange(): failed")
			return
		}
	}

	if p.updateTarget.needUpdate() {
		needDrain = true
		if p.updateTarget.needReboot() {
			needReboot = true
			log.Log.Info("k8s-plugin OnNodeStateChange(): needReboot to update", "target", p.updateTarget)
		} else {
			log.Log.Info("k8s-plugin OnNodeStateChange(): needDrain to update", "target", p.updateTarget)
		}
	}

	return
}

// Apply config change
func (p *K8sPlugin) Apply() error {
	log.Log.Info("k8s-plugin Apply()")
	if err := p.updateSwitchdevService(); err != nil {
		return err
	}

	if vars.UsingSystemdMode {
		if err := p.updateSriovService(); err != nil {
			return err
		}
	}

	for _, systemService := range p.updateTarget.systemServices {
		if err := p.hostHelper.UpdateSystemService(systemService); err != nil {
			return err
		}
	}

	return nil
}

func (p *K8sPlugin) readSwitchdevManifest() error {
	// Read switchdev service
	switchdevBeforeNMService, err := p.hostHelper.ReadServiceManifestFile(switchdevBeforeNMUnitFile)
	if err != nil {
		return err
	}
	switchdevAfterNMService, err := p.hostHelper.ReadServiceManifestFile(switchdevAfterNMUnitFile)
	if err != nil {
		return err
	}

	switchdevBeforeNMService, err = p.hostHelper.RemoveFromService(switchdevBeforeNMService, host.ConditionOpt)
	if err != nil {
		return err
	}
	switchdevAfterNMService, err = p.hostHelper.RemoveFromService(switchdevAfterNMService, host.ConditionOpt)
	if err != nil {
		return err
	}
	p.switchdevBeforeNMService = switchdevBeforeNMService
	p.switchdevAfterNMService = switchdevAfterNMService

	// Read switchdev run script
	switchdevBeforeNMRunScript, err := p.hostHelper.ReadScriptManifestFile(configuresSwitchdevBeforeNMScript)
	if err != nil {
		return err
	}
	switchdevAfterNMRunScript, err := p.hostHelper.ReadScriptManifestFile(configuresSwitchdevAfterNMScript)
	if err != nil {
		return err
	}
	p.switchdevBeforeNMRunScript = switchdevBeforeNMRunScript
	p.switchdevAfterNMRunScript = switchdevAfterNMRunScript

	// Read switchdev udev script
	switchdevUdevScript, err := p.hostHelper.ReadScriptManifestFile(switchdevRenamingUdevScript)
	if err != nil {
		return err
	}
	p.switchdevUdevScript = switchdevUdevScript

	return nil
}

func (p *K8sPlugin) readNetworkManagerManifest() error {
	networkManagerService, err := p.hostHelper.ReadServiceInjectionManifestFile(networkManagerUnitFile)
	if err != nil {
		return err
	}

	p.networkManagerService = networkManagerService
	return nil
}

func (p *K8sPlugin) readOpenVSwitchdManifest() error {
	openVSwitchService, err := p.hostHelper.ReadServiceInjectionManifestFile(ovsUnitFile)
	if err != nil {
		return err
	}

	p.openVSwitchService = openVSwitchService
	return nil
}

func (p *K8sPlugin) readSriovServiceManifest() error {
	sriovService, err := p.hostHelper.ReadServiceManifestFile(sriovUnitFile)
	if err != nil {
		return err
	}

	p.sriovService = sriovService
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

	if err := p.readSriovServiceManifest(); err != nil {
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

func (p *K8sPlugin) sriovServiceStateUpdate() error {
	log.Log.Info("sriovServiceStateUpdate()")
	isServiceEnabled, err := p.hostHelper.IsServiceEnabled(p.sriovService.Path)
	if err != nil {
		return err
	}

	// create and enable the service if it doesn't exist or is not enabled
	if !isServiceEnabled {
		p.updateTarget.sriovScript = true
	} else {
		p.updateTarget.sriovScript = p.isSystemServiceNeedUpdate(p.sriovService)
	}

	if p.updateTarget.sriovScript {
		p.updateTarget.systemServices = append(p.updateTarget.systemServices, p.sriovService)
	}
	return nil
}

func (p *K8sPlugin) getSwitchDevSystemServices() []*host.Service {
	return []*host.Service{p.networkManagerService, p.openVSwitchService}
}

func (p *K8sPlugin) isSwitchdevScriptNeedUpdate(scriptObj *host.ScriptManifestFile) (needUpdate bool, err error) {
	data, err := os.ReadFile(path.Join(consts.Host, scriptObj.Path))
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

func (p *K8sPlugin) isSwitchdevServiceNeedUpdate(serviceObj *host.Service) (needUpdate bool, err error) {
	swdService, err := p.hostHelper.ReadService(serviceObj.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		// service not exists
		return true, nil
	} else {
		needChange, err := p.hostHelper.CompareServices(swdService, serviceObj)
		if err != nil {
			return false, err
		}
		return needChange, nil
	}
}

func (p *K8sPlugin) isSystemServiceNeedUpdate(serviceObj *host.Service) bool {
	log.Log.Info("isSystemServiceNeedUpdate()")
	systemService, err := p.hostHelper.ReadService(serviceObj.Path)
	if err != nil {
		log.Log.Error(err, "k8s-plugin isSystemServiceNeedUpdate(): failed to read sriov-config service file, ignoring",
			"path", serviceObj.Path)
		return false
	}
	if systemService != nil {
		needChange, err := p.hostHelper.CompareServices(systemService, serviceObj)
		if err != nil {
			log.Log.Error(err, "k8s-plugin isSystemServiceNeedUpdate(): failed to compare sriov-config service, ignoring")
			return false
		}
		return needChange
	}

	return false
}

func (p *K8sPlugin) systemServicesStateUpdate() error {
	var services []*host.Service
	for _, systemService := range p.getSwitchDevSystemServices() {
		exist, err := p.hostHelper.IsServiceExist(systemService.Path)
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

func (p *K8sPlugin) switchDevServicesStateUpdate() error {
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

func (p *K8sPlugin) updateSriovService() error {
	if p.updateTarget.sriovScript {
		err := p.hostHelper.EnableService(p.sriovService)
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *K8sPlugin) updateSwitchdevService() error {
	if p.updateTarget.switchdevBeforeNMService {
		err := p.hostHelper.EnableService(p.switchdevBeforeNMService)
		if err != nil {
			return err
		}
	}

	if p.updateTarget.switchdevAfterNMService {
		err := p.hostHelper.EnableService(p.switchdevAfterNMService)
		if err != nil {
			return err
		}
	}

	if p.updateTarget.switchdevBeforeNMRunScript {
		err := os.WriteFile(path.Join(consts.Host, p.switchdevBeforeNMRunScript.Path),
			[]byte(p.switchdevBeforeNMRunScript.Contents.Inline), 0755)
		if err != nil {
			return err
		}
	}

	if p.updateTarget.switchdevAfterNMRunScript {
		err := os.WriteFile(path.Join(consts.Host, p.switchdevAfterNMRunScript.Path),
			[]byte(p.switchdevAfterNMRunScript.Contents.Inline), 0755)
		if err != nil {
			return err
		}
	}

	if p.updateTarget.switchdevUdevScript {
		err := os.WriteFile(path.Join(consts.Host, p.switchdevUdevScript.Path),
			[]byte(p.switchdevUdevScript.Contents.Inline), 0755)
		if err != nil {
			return err
		}
	}

	return nil
}
