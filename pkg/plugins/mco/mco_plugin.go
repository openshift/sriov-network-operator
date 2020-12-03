package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/golang/glog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/api/v1"
	"github.com/openshift/sriov-network-operator/controllers"
)

type McoPlugin struct {
	PluginName  string
	SpecVersion string
	DesireState *sriovnetworkv1.SriovNetworkNodeState
	LastState   *sriovnetworkv1.SriovNetworkNodeState
}

const (
	switchdevUnitPath = "/host/etc/systemd/system/switchdev-configuration.service"
	switchDevConfPath = "/host/etc/switchdev.conf"
	nodeLabelPrefix   = "node-role.kubernetes.io/"
)

var nodeName string
var Plugin McoPlugin
var kubeclient *kubernetes.Clientset
var switchdevConfigured bool

// Initialize our plugin and set up initial values
func init() {
	Plugin = McoPlugin{
		PluginName:  "mco_plugin",
		SpecVersion: "1.0",
	}

	var config *rest.Config
	var err error
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		// creates the in-cluster config
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		panic(err.Error())
	}
	kubeclient = kubernetes.NewForConfigOrDie(config)
}

// Name returns the name of the plugin
func (p *McoPlugin) Name() string {
	return p.PluginName
}

// Spec returns the version of the spec expected by the plugin
func (p *McoPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateAdd Invoked when SriovNetworkNodeState CR is created, return if need dain and/or reboot node
func (p *McoPlugin) OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("mco-plugin OnNodeStateAdd()")
	nodeName = state.GetName()
	return p.OnNodeStateChange(nil, state)
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
func (p *McoPlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("mco-plugin OnNodeStateChange()")
	switchdevConfigured = false
	for _, iface := range new.Spec.Interfaces {
		if iface.EswitchMode == sriovnetworkv1.ESWITCHMODE_SWITCHDEV {
			switchdevConfigured = true
			break
		}
	}

	var update, remove bool
	if update, remove, err = writeSwitchdevConfFile(new); err != nil {
		glog.Errorf("mco-plugin OnNodeStateChange():fail to update switchdev.conf file: %v", err)
		return
	}
	if remove {
		needDrain = true
		return
	}
	if update {
		if _, err = os.Stat(switchdevUnitPath); err != nil {
			if os.IsNotExist(err) {
				glog.Info("mco-plugin OnNodeStateChange(): the latest MachineConfig has not been applied")
				needDrain = true
				err = nil
				return
			}
			return
		}
		// node is already in the offload MCP
		glog.Info("mco-plugin OnNodeStateChange(): need reboot node to use the up-to-date switchdev.conf")
		needDrain = true
		needReboot = true
		return
	}
	return
}

// Apply config change
func (p *McoPlugin) Apply() error {
	glog.Info("mco-plugin Apply()")
	node, err := kubeclient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	label := nodeLabelPrefix + controllers.HwOffloadNodeLabel
	if switchdevConfigured {
		if _, ok := node.Labels[label]; !ok {
			glog.Infof("Move node %s into HW offload MachineConfigPool", node.Name)
			mergePatch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						label: "",
					},
				},
			})
			kubeclient.CoreV1().Nodes().Patch(context.Background(), nodeName, types.MergePatchType, mergePatch, metav1.PatchOptions{})
			return nil
		}
		glog.Infof("Node %s is already in HW offload MachineConfigPool", node.Name)
		return nil
	}

	if _, ok := node.Labels[label]; ok {
		glog.Infof("Remove node %s from HW offload MachineConfigPool", node.Name)
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					label: nil,
				},
			},
		})
		kubeclient.CoreV1().Nodes().Patch(context.Background(), nodeName, types.MergePatchType, mergePatch, metav1.PatchOptions{})
		return nil
	}
	glog.Infof("Node %s is not in HW offload MachineConfigPool", node.Name)
	return nil
}

func writeSwitchdevConfFile(newState *sriovnetworkv1.SriovNetworkNodeState) (update, remove bool, err error) {
	_, err = os.Stat(switchDevConfPath)
	if err != nil {
		if os.IsNotExist(err) {
			glog.V(2).Infof("writeSwitchdevConfFile(): file not existed, create it")
			_, err = os.Create(switchDevConfPath)
			if err != nil {
				glog.Errorf("writeSwitchdevConfFile(): fail to create file: %v", err)
				return
			}
		} else {
			return
		}
	}
	newContent := ""
	for _, iface := range newState.Spec.Interfaces {
		if iface.EswitchMode == sriovnetworkv1.ESWITCHMODE_SWITCHDEV {
			newContent = newContent + fmt.Sprintln(iface.PciAddress, iface.NumVfs)
		}
	}
	oldContent, err := ioutil.ReadFile(switchDevConfPath)
	if err != nil {
		glog.Errorf("writeSwitchdevConfFile(): fail to read file: %v", err)
		return
	}
	if newContent == string(oldContent) {
		glog.V(2).Info("writeSwitchdevConfFile(): no update")
		return
	}
	if newContent == "" {
		remove = true
		glog.V(2).Info("writeSwitchdevConfFile(): remove content in switchdev.conf")
	}
	update = true
	glog.V(2).Infof("writeSwitchdevConfFile(): write %s to switchdev.conf", newContent)
	err = ioutil.WriteFile(switchDevConfPath, []byte(newContent), 0666)
	if err != nil {
		glog.Errorf("writeSwitchdevConfFile(): fail to write file: %v", err)
		return
	}
	return
}
