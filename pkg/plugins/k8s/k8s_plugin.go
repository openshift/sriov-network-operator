package k8s

import (
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	hostTypes "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	plugins "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var PluginName = "k8s"

type K8sPlugin struct {
	PluginName  string
	SpecVersion string
	hostHelper  helper.HostHelpersInterface

	openVSwitchService      *hostTypes.Service
	sriovService            *hostTypes.Service
	sriovPostNetworkService *hostTypes.Service

	updateTarget *k8sUpdateTarget
}
type updateTargetReq struct {
	update bool
	reboot bool
}

// set need update flag for updateTargetReq
func (u *updateTargetReq) SetNeedUpdate() {
	u.update = true
}

// set need update and reboot flags for updateTargetReq
func (u *updateTargetReq) SetNeedReboot() {
	u.update = true
	u.reboot = true
}

// returns state of the update flag
func (u *updateTargetReq) NeedUpdate() bool {
	return u.update
}

// returns state of the reboot flag
func (u *updateTargetReq) NeedReboot() bool {
	return u.reboot
}

type k8sUpdateTarget struct {
	sriovScript            updateTargetReq
	sriovPostNetworkScript updateTargetReq
	openVSwitch            updateTargetReq
}

func (u *k8sUpdateTarget) String() string {
	var updateList []string
	if u.sriovScript.NeedReboot() {
		updateList = append(updateList, "sriov-config.service")
	}
	if u.sriovPostNetworkScript.NeedReboot() {
		updateList = append(updateList, "sriov-config-post-network.service")
	}
	if u.openVSwitch.NeedReboot() {
		updateList = append(updateList, "ovs-vswitchd.service")
	}
	return strings.Join(updateList, ",")
}

func (u *k8sUpdateTarget) needReboot() bool {
	return u.sriovScript.NeedReboot() || u.sriovPostNetworkScript.NeedReboot() || u.openVSwitch.NeedReboot()
}

func (u *k8sUpdateTarget) reset() {
	u.sriovScript = updateTargetReq{}
	u.sriovPostNetworkScript = updateTargetReq{}
	u.openVSwitch = updateTargetReq{}
}

const (
	bindataManifestPath      = "bindata/manifests/"
	switchdevManifestPath    = bindataManifestPath + "switchdev-config/"
	switchdevUnits           = switchdevManifestPath + "switchdev-units/"
	sriovUnits               = bindataManifestPath + "sriov-config-service/kubernetes/"
	sriovUnitFile            = sriovUnits + "sriov-config-service.yaml"
	sriovPostNetworkUnitFile = sriovUnits + "sriov-config-post-network-service.yaml"
	ovsUnitFile              = switchdevManifestPath + "ovs-units/ovs-vswitchd.service.yaml"
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
	log.Log.Info("k8s plugin OnNodeStateChange()")
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
		err = p.ovsServiceStateUpdate()
		if err != nil {
			log.Log.Error(err, "k8s plugin OnNodeStateChange(): failed")
			return
		}
	}

	if vars.UsingSystemdMode {
		// Check sriov service
		err = p.sriovServicesStateUpdate()
		if err != nil {
			log.Log.Error(err, "k8s plugin OnNodeStateChange(): failed")
			return
		}
	}

	if p.updateTarget.needReboot() {
		needDrain = true
		needReboot = true
		log.Log.Info("k8s plugin OnNodeStateChange(): needReboot to update", "target", p.updateTarget)
	}

	return
}

// TODO: implement - https://github.com/k8snetworkplumbingwg/sriov-network-operator/issues/630
// OnNodeStatusChange verify whether SriovNetworkNodeState CR status present changes on configured VFs.
func (p *K8sPlugin) CheckStatusChanges(*sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	return false, nil
}

// Apply config change
func (p *K8sPlugin) Apply() error {
	log.Log.Info("k8s plugin Apply()")
	if vars.UsingSystemdMode {
		if err := p.updateSriovServices(); err != nil {
			return err
		}
	}
	return p.updateOVSService()
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

func (p *K8sPlugin) readSriovPostNetworkServiceManifest() error {
	sriovService, err := p.hostHelper.ReadServiceManifestFile(sriovPostNetworkUnitFile)
	if err != nil {
		return err
	}
	p.sriovPostNetworkService = sriovService
	return nil
}

func (p *K8sPlugin) readManifestFiles() error {
	if err := p.readOpenVSwitchdManifest(); err != nil {
		return err
	}
	if err := p.readSriovServiceManifest(); err != nil {
		return err
	}
	if err := p.readSriovPostNetworkServiceManifest(); err != nil {
		return err
	}
	return nil
}

func (p *K8sPlugin) sriovServicesStateUpdate() error {
	for _, s := range []struct {
		srv    *hostTypes.Service
		update *updateTargetReq
	}{
		{srv: p.sriovService, update: &p.updateTarget.sriovScript},
		{srv: p.sriovPostNetworkService, update: &p.updateTarget.sriovPostNetworkScript},
	} {
		isServiceEnabled, err := p.hostHelper.IsServiceEnabled(s.srv.Path)
		if err != nil {
			return err
		}
		// create and enable the service if it doesn't exist or is not enabled
		if !isServiceEnabled {
			s.update.SetNeedReboot()
		} else {
			if p.isSystemDServiceNeedUpdate(s.srv) {
				s.update.SetNeedReboot()
			}
		}
	}
	return nil
}

func (p *K8sPlugin) updateSriovServices() error {
	for _, s := range []struct {
		srv    *hostTypes.Service
		update *updateTargetReq
	}{
		{srv: p.sriovService, update: &p.updateTarget.sriovScript},
		{srv: p.sriovPostNetworkService, update: &p.updateTarget.sriovPostNetworkScript},
	} {
		if s.update.NeedUpdate() {
			err := p.hostHelper.EnableService(s.srv)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *K8sPlugin) ovsServiceStateUpdate() error {
	exist, err := p.hostHelper.IsServiceExist(p.openVSwitchService.Path)
	if err != nil {
		return err
	}
	if !exist {
		log.Log.Info("k8s plugin systemServicesStateUpdate(): WARNING! openvswitch system service not found, skip update",
			"service", p.openVSwitchService.Name)
		return nil
	}
	if !p.isSystemDServiceNeedUpdate(p.openVSwitchService) {
		// service is up to date
		return nil
	}
	if p.isOVSHwOffloadingEnabled() {
		p.updateTarget.openVSwitch.SetNeedUpdate()
	} else {
		p.updateTarget.openVSwitch.SetNeedReboot()
	}
	return nil
}

func (p *K8sPlugin) updateOVSService() error {
	if p.updateTarget.openVSwitch.NeedUpdate() {
		return p.hostHelper.UpdateSystemService(p.openVSwitchService)
	}
	return nil
}

func (p *K8sPlugin) isSystemDServiceNeedUpdate(serviceObj *hostTypes.Service) bool {
	systemService, err := p.hostHelper.ReadService(serviceObj.Path)
	if err != nil {
		log.Log.Error(err, "k8s plugin isSystemDServiceNeedUpdate(): failed to read service file, ignoring",
			"path", serviceObj.Path)
		return false
	}
	if systemService != nil {
		needChange, err := p.hostHelper.CompareServices(systemService, serviceObj)
		if err != nil {
			log.Log.Error(err, "k8s plugin isSystemDServiceNeedUpdate(): failed to compare service, ignoring")
			return false
		}
		return needChange
	}
	return false
}

// try to check if OVS HW offloading is already enabled
// required to avoid unneeded reboots in case if HW offloading is already enabled by different entity
// TODO move to the right package and avoid ovs-vsctl binary call
// the function should be revisited when support for software bridge configuration
// is implemented
func (p *K8sPlugin) isOVSHwOffloadingEnabled() bool {
	log.Log.V(2).Info("isOVSHwOffloadingEnabled()")
	exit, err := p.hostHelper.Chroot(consts.Chroot)
	if err != nil {
		return false
	}
	defer exit()
	out, _, err := p.hostHelper.RunCommand("ovs-vsctl", "get", "Open_vSwitch", ".", "other_config:hw-offload")
	if err != nil {
		log.Log.V(2).Info("isOVSHwOffloadingEnabled() check failed, assume offloading is disabled", "error", err.Error())
		return false
	}
	if strings.Trim(out, "\n") == `"true"` {
		log.Log.V(2).Info("isOVSHwOffloadingEnabled() offloading is already enabled")
		return true
	}
	return false
}
