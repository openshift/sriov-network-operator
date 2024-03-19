package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

const (
	LASTNETWORKNAMESPACE    = "operator.sriovnetwork.openshift.io/last-network-namespace"
	NETATTDEFFINALIZERNAME  = "netattdef.finalizers.sriovnetwork.openshift.io"
	POOLCONFIGFINALIZERNAME = "poolconfig.finalizers.sriovnetwork.openshift.io"
	ESwithModeLegacy        = "legacy"
	ESwithModeSwitchDev     = "switchdev"

	SriovCniStateEnable  = "enable"
	SriovCniStateDisable = "disable"
	SriovCniStateAuto    = "auto"
	SriovCniStateOff     = "off"
	SriovCniStateOn      = "on"
	SriovCniIpam         = "\"ipam\""
	SriovCniIpamEmpty    = SriovCniIpam + ":{}"
)

const invalidVfIndex = -1

var ManifestsPath = "./bindata/manifests/cni-config"
var log = logf.Log.WithName("sriovnetwork")

// NicIDMap contains supported mapping of IDs with each in the format of:
// Vendor ID, Physical Function Device ID, Virtual Function Device ID
var NicIDMap = []string{}

var InitialState SriovNetworkNodeState

// NetFilterType Represents the NetFilter tags to be used
type NetFilterType int

const (
	// OpenstackNetworkID network UUID
	OpenstackNetworkID NetFilterType = iota

	SupportedNicIDConfigmap = "supported-nic-ids"
)

type ConfigurationModeType string

const (
	DaemonConfigurationMode  ConfigurationModeType = "daemon"
	SystemdConfigurationMode ConfigurationModeType = "systemd"
)

func (e NetFilterType) String() string {
	switch e {
	case OpenstackNetworkID:
		return "openstack/NetworkID"
	default:
		return fmt.Sprintf("%d", int(e))
	}
}

func InitNicIDMapFromConfigMap(client kubernetes.Interface, namespace string) error {
	cm, err := client.CoreV1().ConfigMaps(namespace).Get(
		context.Background(),
		SupportedNicIDConfigmap,
		metav1.GetOptions{},
	)
	// if the configmap does not exist, return false
	if err != nil {
		return err
	}
	for _, v := range cm.Data {
		NicIDMap = append(NicIDMap, v)
	}

	return nil
}

func InitNicIDMapFromList(idList []string) {
	NicIDMap = append(NicIDMap, idList...)
}

func IsSupportedVendor(vendorID string) bool {
	for _, n := range NicIDMap {
		ids := strings.Split(n, " ")
		if vendorID == ids[0] {
			return true
		}
	}
	return false
}

func IsSupportedDevice(deviceID string) bool {
	for _, n := range NicIDMap {
		ids := strings.Split(n, " ")
		if deviceID == ids[1] {
			return true
		}
	}
	return false
}

func IsSupportedModel(vendorID, deviceID string) bool {
	for _, n := range NicIDMap {
		ids := strings.Split(n, " ")
		if vendorID == ids[0] && deviceID == ids[1] {
			return true
		}
	}
	log.Info("IsSupportedModel(): found unsupported model", "vendorId:", vendorID, "deviceId:", deviceID)
	return false
}

func IsVfSupportedModel(vendorID, deviceID string) bool {
	for _, n := range NicIDMap {
		ids := strings.Split(n, " ")
		if vendorID == ids[0] && deviceID == ids[2] {
			return true
		}
	}
	log.Info("IsVfSupportedModel(): found unsupported VF model", "vendorId:", vendorID, "deviceId:", deviceID)
	return false
}

func IsEnabledUnsupportedVendor(vendorID string, unsupportedNicIDMap map[string]string) bool {
	for _, n := range unsupportedNicIDMap {
		if IsValidPciString(n) {
			ids := strings.Split(n, " ")
			if vendorID == ids[0] {
				return true
			}
		}
	}
	return false
}

func IsValidPciString(nicIDString string) bool {
	ids := strings.Split(nicIDString, " ")

	if len(ids) != 3 {
		log.Info("IsValidPciString(): ", nicIDString)
		return false
	}

	if len(ids[0]) != 4 {
		log.Info("IsValidPciString():", "Invalid vendor PciId ", ids[0])
		return false
	}
	if _, err := strconv.ParseInt(ids[0], 16, 32); err != nil {
		log.Info("IsValidPciString():", "Invalid vendor PciId ", ids[0])
	}

	if len(ids[1]) != 4 {
		log.Info("IsValidPciString():", "Invalid PciId of PF ", ids[1])
		return false
	}
	if _, err := strconv.ParseInt(ids[1], 16, 32); err != nil {
		log.Info("IsValidPciString():", "Invalid PciId of PF ", ids[1])
	}

	if len(ids[2]) != 4 {
		log.Info("IsValidPciString():", "Invalid PciId of VF ", ids[2])
		return false
	}
	if _, err := strconv.ParseInt(ids[2], 16, 32); err != nil {
		log.Info("IsValidPciString():", "Invalid PciId of VF ", ids[2])
	}

	return true
}

func GetSupportedVfIds() []string {
	var vfIds []string
	for _, n := range NicIDMap {
		ids := strings.Split(n, " ")
		vfID := "0x" + ids[2]
		if !StringInArray(vfID, vfIds) {
			vfIds = append(vfIds, vfID)
		}
	}
	// return a sorted slice so that udev rule is stable
	sort.Slice(vfIds, func(i, j int) bool {
		ip, _ := strconv.ParseInt(vfIds[i], 0, 32)
		jp, _ := strconv.ParseInt(vfIds[j], 0, 32)
		return ip < jp
	})
	return vfIds
}

func GetVfDeviceID(deviceID string) string {
	for _, n := range NicIDMap {
		ids := strings.Split(n, " ")
		if deviceID == ids[1] {
			return ids[2]
		}
	}
	return ""
}

func IsSwitchdevModeSpec(spec SriovNetworkNodeStateSpec) bool {
	for _, iface := range spec.Interfaces {
		if iface.EswitchMode == ESwithModeSwitchDev {
			return true
		}
	}
	return false
}

func FindInterface(interfaces Interfaces, name string) (iface Interface, err error) {
	for _, i := range interfaces {
		if i.Name == name {
			return i, nil
		}
	}
	return Interface{}, fmt.Errorf("unable to find interface: %v", name)
}

// GetEswitchModeFromSpec returns ESwitchMode from the interface spec, returns legacy if not set
func GetEswitchModeFromSpec(ifaceSpec *Interface) string {
	if ifaceSpec.EswitchMode == "" {
		return ESwithModeLegacy
	}
	return ifaceSpec.EswitchMode
}

// GetEswitchModeFromStatus returns ESwitchMode from the interface status, returns legacy if not set
func GetEswitchModeFromStatus(ifaceStatus *InterfaceExt) string {
	if ifaceStatus.EswitchMode == "" {
		return ESwithModeLegacy
	}
	return ifaceStatus.EswitchMode
}

func NeedToUpdateSriov(ifaceSpec *Interface, ifaceStatus *InterfaceExt) bool {
	if ifaceSpec.Mtu > 0 {
		mtu := ifaceSpec.Mtu
		if mtu != ifaceStatus.Mtu {
			log.V(2).Info("NeedToUpdateSriov(): MTU needs update", "desired", mtu, "current", ifaceStatus.Mtu)
			return true
		}
	}

	if ifaceSpec.NumVfs != ifaceStatus.NumVfs {
		log.V(2).Info("NeedToUpdateSriov(): NumVfs needs update", "desired", ifaceSpec.NumVfs, "current", ifaceStatus.NumVfs)
		return true
	}
	if ifaceSpec.NumVfs > 0 {
		for _, vfStatus := range ifaceStatus.VFs {
			ingroup := false
			for _, groupSpec := range ifaceSpec.VfGroups {
				if IndexInRange(vfStatus.VfID, groupSpec.VfRange) {
					ingroup = true
					if vfStatus.Driver == "" {
						log.V(2).Info("NeedToUpdateSriov(): Driver needs update - has no driver",
							"desired", groupSpec.DeviceType)
						return true
					}
					if groupSpec.DeviceType != "" && groupSpec.DeviceType != consts.DeviceTypeNetDevice {
						if groupSpec.DeviceType != vfStatus.Driver {
							log.V(2).Info("NeedToUpdateSriov(): Driver needs update",
								"desired", groupSpec.DeviceType, "current", vfStatus.Driver)
							return true
						}
					} else {
						if StringInArray(vfStatus.Driver, vars.DpdkDrivers) {
							log.V(2).Info("NeedToUpdateSriov(): Driver needs update",
								"desired", groupSpec.DeviceType, "current", vfStatus.Driver)
							return true
						}
						if vfStatus.Mtu != 0 && groupSpec.Mtu != 0 && vfStatus.Mtu != groupSpec.Mtu {
							log.V(2).Info("NeedToUpdateSriov(): VF MTU needs update",
								"vf", vfStatus.VfID, "desired", groupSpec.Mtu, "current", vfStatus.Mtu)
							return true
						}

						// this is needed to be sure the admin mac address is configured as expected
						if ifaceSpec.ExternallyManaged {
							log.V(2).Info("NeedToUpdateSriov(): need to update the device as it's externally manage",
								"device", ifaceStatus.PciAddress)
							return true
						}
					}
					break
				}
			}
			if !ingroup && StringInArray(vfStatus.Driver, vars.DpdkDrivers) {
				// VF which has DPDK driver loaded but not in any group, needs to be reset to default driver.
				return true
			}
		}
	}
	return false
}

type ByPriority []SriovNetworkNodePolicy

func (a ByPriority) Len() int {
	return len(a)
}

func (a ByPriority) Less(i, j int) bool {
	if a[i].Spec.Priority != a[j].Spec.Priority {
		return a[i].Spec.Priority > a[j].Spec.Priority
	}
	return a[i].GetName() < a[j].GetName()
}

func (a ByPriority) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

// Match check if node is selected by NodeSelector
func (p *SriovNetworkNodePolicy) Selected(node *corev1.Node) bool {
	for k, v := range p.Spec.NodeSelector {
		if nv, ok := node.Labels[k]; ok && nv == v {
			continue
		}
		return false
	}
	return true
}

func StringInArray(val string, array []string) bool {
	for i := range array {
		if array[i] == val {
			return true
		}
	}
	return false
}

func RemoveString(s string, slice []string) (result []string, found bool) {
	if len(slice) != 0 {
		for _, item := range slice {
			if item == s {
				found = true
				continue
			}
			result = append(result, item)
		}
	}
	return
}

func UniqueAppend(inSlice []string, strings ...string) []string {
	for _, s := range strings {
		if !StringInArray(s, inSlice) {
			inSlice = append(inSlice, s)
		}
	}
	return inSlice
}

// Apply policy to SriovNetworkNodeState CR
func (p *SriovNetworkNodePolicy) Apply(state *SriovNetworkNodeState, equalPriority bool) error {
	s := p.Spec.NicSelector
	if s.Vendor == "" && s.DeviceID == "" && len(s.RootDevices) == 0 && len(s.PfNames) == 0 &&
		len(s.NetFilter) == 0 {
		// Empty NicSelector match none
		return nil
	}
	for _, iface := range state.Status.Interfaces {
		if s.Selected(&iface) {
			log.Info("Update interface", "name:", iface.Name)
			result := Interface{
				PciAddress:        iface.PciAddress,
				Mtu:               p.Spec.Mtu,
				Name:              iface.Name,
				LinkType:          p.Spec.LinkType,
				EswitchMode:       p.Spec.EswitchMode,
				NumVfs:            p.Spec.NumVfs,
				ExternallyManaged: p.Spec.ExternallyManaged,
			}
			if p.Spec.NumVfs > 0 {
				group, err := p.generateVfGroup(&iface)
				if err != nil {
					return err
				}
				result.VfGroups = []VfGroup{*group}
				found := false
				for i := range state.Spec.Interfaces {
					if state.Spec.Interfaces[i].PciAddress == result.PciAddress {
						found = true
						state.Spec.Interfaces[i].mergeConfigs(&result, equalPriority)
						state.Spec.Interfaces[i] = result
						break
					}
				}
				if !found {
					state.Spec.Interfaces = append(state.Spec.Interfaces, result)
				}
			}
		}
	}
	return nil
}

// mergeConfigs merges configs from multiple polices where the last one has the
// highest priority. This merge is dependent on: 1. SR-IOV partition is
// configured with the #-notation in pfName, 2. The VF groups are
// non-overlapping or SR-IOV policies have the same priority.
func (iface Interface) mergeConfigs(input *Interface, equalPriority bool) {
	m := false
	// merge VF groups (input.VfGroups already contains the highest priority):
	// - skip group with same ResourceName,
	// - skip overlapping groups (use only highest priority)
	for _, gr := range iface.VfGroups {
		if gr.ResourceName == input.VfGroups[0].ResourceName || gr.isVFRangeOverlapping(input.VfGroups[0]) {
			continue
		}
		m = true
		input.VfGroups = append(input.VfGroups, gr)
	}

	if !equalPriority && !m {
		return
	}

	// mtu configuration we take the highest value
	if input.Mtu < iface.Mtu {
		input.Mtu = iface.Mtu
	}
	if input.NumVfs < iface.NumVfs {
		input.NumVfs = iface.NumVfs
	}
}

func (gr VfGroup) isVFRangeOverlapping(group VfGroup) bool {
	rngSt, rngEnd, err := parseRange(gr.VfRange)
	if err != nil {
		return false
	}
	rngSt2, rngEnd2, err := parseRange(group.VfRange)
	if err != nil {
		return false
	}
	// compare minimal range has overlap
	if rngSt < rngSt2 {
		return IndexInRange(rngSt2, gr.VfRange) || IndexInRange(rngEnd2, gr.VfRange)
	}
	return IndexInRange(rngSt, group.VfRange) || IndexInRange(rngEnd, group.VfRange)
}

func (p *SriovNetworkNodePolicy) generateVfGroup(iface *InterfaceExt) (*VfGroup, error) {
	var err error
	pfName := ""
	var rngStart, rngEnd int
	found := false
	for _, selector := range p.Spec.NicSelector.PfNames {
		pfName, rngStart, rngEnd, err = ParsePFName(selector)
		if err != nil {
			log.Error(err, "Unable to parse PF Name.")
			return nil, err
		}
		if pfName == iface.Name {
			found = true
			if rngStart == invalidVfIndex && rngEnd == invalidVfIndex {
				rngStart, rngEnd = 0, p.Spec.NumVfs-1
			}
			break
		}
	}
	if !found {
		// assign the default vf index range if the pfName is not specified by the nicSelector
		rngStart, rngEnd = 0, p.Spec.NumVfs-1
	}
	rng := strconv.Itoa(rngStart) + "-" + strconv.Itoa(rngEnd)
	return &VfGroup{
		ResourceName: p.Spec.ResourceName,
		DeviceType:   p.Spec.DeviceType,
		VfRange:      rng,
		PolicyName:   p.GetName(),
		Mtu:          p.Spec.Mtu,
		IsRdma:       p.Spec.IsRdma,
		VdpaType:     p.Spec.VdpaType,
	}, nil
}

func IndexInRange(i int, r string) bool {
	rngSt, rngEnd, err := parseRange(r)
	if err != nil {
		return false
	}
	if i <= rngEnd && i >= rngSt {
		return true
	}
	return false
}

func parseRange(r string) (rngSt, rngEnd int, err error) {
	rng := strings.Split(r, "-")
	rngSt, err = strconv.Atoi(rng[0])
	if err != nil {
		return
	}
	rngEnd, err = strconv.Atoi(rng[1])
	if err != nil {
		return
	}
	return
}

// Parse PF name with VF range
func ParsePFName(name string) (ifName string, rngSt, rngEnd int, err error) {
	rngSt, rngEnd = invalidVfIndex, invalidVfIndex
	if strings.Contains(name, "#") {
		fields := strings.Split(name, "#")
		ifName = fields[0]
		rngSt, rngEnd, err = parseRange(fields[1])
	} else {
		ifName = name
	}
	return
}

func (selector *SriovNetworkNicSelector) Selected(iface *InterfaceExt) bool {
	if selector.Vendor != "" && selector.Vendor != iface.Vendor {
		return false
	}
	if selector.DeviceID != "" && selector.DeviceID != iface.DeviceID {
		return false
	}
	if len(selector.RootDevices) > 0 && !StringInArray(iface.PciAddress, selector.RootDevices) {
		return false
	}
	if len(selector.PfNames) > 0 {
		var pfNames []string
		for _, p := range selector.PfNames {
			if strings.Contains(p, "#") {
				fields := strings.Split(p, "#")
				pfNames = append(pfNames, fields[0])
			} else {
				pfNames = append(pfNames, p)
			}
		}
		if !StringInArray(iface.Name, pfNames) {
			return false
		}
	}
	if selector.NetFilter != "" && !NetFilterMatch(selector.NetFilter, iface.NetFilter) {
		return false
	}

	return true
}

func (s *SriovNetworkNodeState) GetInterfaceStateByPciAddress(addr string) *InterfaceExt {
	for _, iface := range s.Status.Interfaces {
		if addr == iface.PciAddress {
			return &iface
		}
	}
	return nil
}

func (s *SriovNetworkNodeState) GetDriverByPciAddress(addr string) string {
	for _, iface := range s.Status.Interfaces {
		if addr == iface.PciAddress {
			return iface.Driver
		}
	}
	return ""
}

// RenderNetAttDef renders a net-att-def for ib-sriov CNI
func (cr *SriovIBNetwork) RenderNetAttDef() (*uns.Unstructured, error) {
	logger := log.WithName("RenderNetAttDef")
	logger.Info("Start to render IB SRIOV CNI NetworkAttachmentDefinition")

	// render RawCNIConfig manifests
	data := render.MakeRenderData()
	data.Data["CniType"] = "ib-sriov"
	data.Data["SriovNetworkName"] = cr.Name
	if cr.Spec.NetworkNamespace == "" {
		data.Data["SriovNetworkNamespace"] = cr.Namespace
	} else {
		data.Data["SriovNetworkNamespace"] = cr.Spec.NetworkNamespace
	}
	data.Data["SriovCniResourceName"] = os.Getenv("RESOURCE_PREFIX") + "/" + cr.Spec.ResourceName

	data.Data["StateConfigured"] = true
	switch cr.Spec.LinkState {
	case SriovCniStateEnable:
		data.Data["SriovCniState"] = SriovCniStateEnable
	case SriovCniStateDisable:
		data.Data["SriovCniState"] = SriovCniStateDisable
	case SriovCniStateAuto:
		data.Data["SriovCniState"] = SriovCniStateAuto
	default:
		data.Data["StateConfigured"] = false
	}

	if cr.Spec.Capabilities == "" {
		data.Data["CapabilitiesConfigured"] = false
	} else {
		data.Data["CapabilitiesConfigured"] = true
		data.Data["SriovCniCapabilities"] = cr.Spec.Capabilities
	}

	if cr.Spec.IPAM != "" {
		data.Data["SriovCniIpam"] = SriovCniIpam + ":" + strings.Join(strings.Fields(cr.Spec.IPAM), "")
	} else {
		data.Data["SriovCniIpam"] = SriovCniIpamEmpty
	}

	// metaplugins for the infiniband cni
	data.Data["MetaPluginsConfigured"] = false
	if cr.Spec.MetaPluginsConfig != "" {
		data.Data["MetaPluginsConfigured"] = true
		data.Data["MetaPlugins"] = cr.Spec.MetaPluginsConfig
	}

	// logLevel and logFile are currently not supports by the ip-sriov-cni -> hardcode them to false.
	data.Data["LogLevelConfigured"] = false
	data.Data["LogFileConfigured"] = false

	objs, err := render.RenderDir(ManifestsPath, &data)
	if err != nil {
		return nil, err
	}
	for _, obj := range objs {
		raw, _ := json.Marshal(obj)
		logger.Info("render NetworkAttachmentDefinition output", "raw", string(raw))
	}
	return objs[0], nil
}

// DeleteNetAttDef deletes the generated net-att-def CR
func (cr *SriovIBNetwork) DeleteNetAttDef(c client.Client) error {
	// Fetch the NetworkAttachmentDefinition instance
	instance := &netattdefv1.NetworkAttachmentDefinition{}
	namespace := cr.GetNamespace()
	if cr.Spec.NetworkNamespace != "" {
		namespace = cr.Spec.NetworkNamespace
	}
	err := c.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: cr.GetName()}, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	err = c.Delete(context.TODO(), instance)
	if err != nil {
		return err
	}
	return nil
}

// RenderNetAttDef renders a net-att-def for sriov CNI
func (cr *SriovNetwork) RenderNetAttDef() (*uns.Unstructured, error) {
	logger := log.WithName("RenderNetAttDef")
	logger.Info("Start to render SRIOV CNI NetworkAttachmentDefinition")

	// render RawCNIConfig manifests
	data := render.MakeRenderData()
	data.Data["CniType"] = "sriov"
	data.Data["SriovNetworkName"] = cr.Name
	if cr.Spec.NetworkNamespace == "" {
		data.Data["SriovNetworkNamespace"] = cr.Namespace
	} else {
		data.Data["SriovNetworkNamespace"] = cr.Spec.NetworkNamespace
	}
	data.Data["SriovCniResourceName"] = os.Getenv("RESOURCE_PREFIX") + "/" + cr.Spec.ResourceName
	data.Data["SriovCniVlan"] = cr.Spec.Vlan

	if cr.Spec.VlanQoS <= 7 && cr.Spec.VlanQoS >= 0 {
		data.Data["VlanQoSConfigured"] = true
		data.Data["SriovCniVlanQoS"] = cr.Spec.VlanQoS
	} else {
		data.Data["VlanQoSConfigured"] = false
	}

	data.Data["VlanProtoConfigured"] = false
	if cr.Spec.VlanProto != "" {
		data.Data["VlanProtoConfigured"] = true
		data.Data["SriovCniVlanProto"] = cr.Spec.VlanProto
	}

	if cr.Spec.Capabilities == "" {
		data.Data["CapabilitiesConfigured"] = false
	} else {
		data.Data["CapabilitiesConfigured"] = true
		data.Data["SriovCniCapabilities"] = cr.Spec.Capabilities
	}

	data.Data["SpoofChkConfigured"] = true
	switch cr.Spec.SpoofChk {
	case SriovCniStateOff:
		data.Data["SriovCniSpoofChk"] = SriovCniStateOff
	case SriovCniStateOn:
		data.Data["SriovCniSpoofChk"] = SriovCniStateOn
	default:
		data.Data["SpoofChkConfigured"] = false
	}

	data.Data["TrustConfigured"] = true
	switch cr.Spec.Trust {
	case SriovCniStateOn:
		data.Data["SriovCniTrust"] = SriovCniStateOn
	case SriovCniStateOff:
		data.Data["SriovCniTrust"] = SriovCniStateOff
	default:
		data.Data["TrustConfigured"] = false
	}

	data.Data["StateConfigured"] = true
	switch cr.Spec.LinkState {
	case SriovCniStateEnable:
		data.Data["SriovCniState"] = SriovCniStateEnable
	case SriovCniStateDisable:
		data.Data["SriovCniState"] = SriovCniStateDisable
	case SriovCniStateAuto:
		data.Data["SriovCniState"] = SriovCniStateAuto
	default:
		data.Data["StateConfigured"] = false
	}

	data.Data["MinTxRateConfigured"] = false
	if cr.Spec.MinTxRate != nil {
		if *cr.Spec.MinTxRate >= 0 {
			data.Data["MinTxRateConfigured"] = true
			data.Data["SriovCniMinTxRate"] = *cr.Spec.MinTxRate
		}
	}

	data.Data["MaxTxRateConfigured"] = false
	if cr.Spec.MaxTxRate != nil {
		if *cr.Spec.MaxTxRate >= 0 {
			data.Data["MaxTxRateConfigured"] = true
			data.Data["SriovCniMaxTxRate"] = *cr.Spec.MaxTxRate
		}
	}

	if cr.Spec.IPAM != "" {
		data.Data["SriovCniIpam"] = SriovCniIpam + ":" + strings.Join(strings.Fields(cr.Spec.IPAM), "")
	} else {
		data.Data["SriovCniIpam"] = SriovCniIpamEmpty
	}

	data.Data["MetaPluginsConfigured"] = false
	if cr.Spec.MetaPluginsConfig != "" {
		data.Data["MetaPluginsConfigured"] = true
		data.Data["MetaPlugins"] = cr.Spec.MetaPluginsConfig
	}

	data.Data["LogLevelConfigured"] = (cr.Spec.LogLevel != "")
	data.Data["LogLevel"] = cr.Spec.LogLevel
	data.Data["LogFileConfigured"] = (cr.Spec.LogFile != "")
	data.Data["LogFile"] = cr.Spec.LogFile

	objs, err := render.RenderDir(ManifestsPath, &data)
	if err != nil {
		return nil, err
	}
	for _, obj := range objs {
		raw, _ := json.Marshal(obj)
		logger.Info("render NetworkAttachmentDefinition output", "raw", string(raw))
	}
	return objs[0], nil
}

// DeleteNetAttDef deletes the generated net-att-def CR
func (cr *SriovNetwork) DeleteNetAttDef(c client.Client) error {
	// Fetch the NetworkAttachmentDefinition instance
	instance := &netattdefv1.NetworkAttachmentDefinition{}
	namespace := cr.GetNamespace()
	if cr.Spec.NetworkNamespace != "" {
		namespace = cr.Spec.NetworkNamespace
	}
	err := c.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: cr.GetName()}, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	err = c.Delete(context.TODO(), instance)
	if err != nil {
		return err
	}
	return nil
}

// NetFilterMatch -- parse netFilter and check for a match
func NetFilterMatch(netFilter string, netValue string) (isMatch bool) {
	logger := log.WithName("NetFilterMatch")

	var re = regexp.MustCompile(`(?m)^\s*([^\s]+)\s*:\s*([^\s]+)`)

	netFilterResult := re.FindAllStringSubmatch(netFilter, -1)

	if netFilterResult == nil {
		logger.Info("Invalid NetFilter spec...", "netFilter", netFilter)
		return false
	}

	netValueResult := re.FindAllStringSubmatch(netValue, -1)

	if netValueResult == nil {
		logger.Info("Invalid netValue...", "netValue", netValue)
		return false
	}

	return netFilterResult[0][1] == netValueResult[0][1] && netFilterResult[0][2] == netValueResult[0][2]
}

// MaxUnavailable calculate the max number of unavailable nodes to represent the number of nodes
// we can drain in parallel
func (s *SriovNetworkPoolConfig) MaxUnavailable(numOfNodes int) (int, error) {
	// this means we want to drain all the nodes in parallel
	if s.Spec.MaxUnavailable == nil {
		return -1, nil
	}
	intOrPercent := *s.Spec.MaxUnavailable

	if intOrPercent.Type == intstrutil.String {
		if strings.HasSuffix(intOrPercent.StrVal, "%") {
			i := strings.TrimSuffix(intOrPercent.StrVal, "%")
			v, err := strconv.Atoi(i)
			if err != nil {
				return 0, fmt.Errorf("invalid value %q: %v", intOrPercent.StrVal, err)
			}
			if v > 100 || v < 1 {
				return 0, fmt.Errorf("invalid value: percentage needs to be between 1 and 100")
			}
		} else {
			return 0, fmt.Errorf("invalid type: strings needs to be a percentage")
		}
	}

	maxunavail, err := intstrutil.GetScaledValueFromIntOrPercent(&intOrPercent, numOfNodes, false)
	if err != nil {
		return 0, err
	}

	if maxunavail < 0 {
		return 0, fmt.Errorf("negative number is not allowed")
	}

	return maxunavail, nil
}
