package ovs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/database/inmemory"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	"github.com/ovn-org/libovsdb/server"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	ovsStoreMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/bridge/ovs/store/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func getManagedBridges() map[string]*sriovnetworkv1.OVSConfigExt {
	return map[string]*sriovnetworkv1.OVSConfigExt{
		"br-0000_d8_00.0": {
			Name: "br-0000_d8_00.0",
			Bridge: sriovnetworkv1.OVSBridgeConfig{
				DatapathType: "netdev",
				ExternalIDs:  map[string]string{"br_externalID_key": "br_externalID_value"},
				OtherConfig:  map[string]string{"br_otherConfig_key": "br_otherConfig_value"},
			},
			Uplinks: []sriovnetworkv1.OVSUplinkConfigExt{{
				PciAddress: "0000:d8:00.0",
				Name:       "enp216s0f0np0",
				Interface: sriovnetworkv1.OVSInterfaceConfig{
					Type:        "dpdk",
					ExternalIDs: map[string]string{"iface_externalID_key": "iface_externalID_value"},
					OtherConfig: map[string]string{"iface_otherConfig_key": "iface_otherConfig_value"},
					Options:     map[string]string{"iface_options_key": "iface_options_value"},
				},
			}},
		},
	}
}

type testDBEntries struct {
	OpenVSwitch []*OpenvSwitchEntry
	Bridge      []*BridgeEntry
	Port        []*PortEntry
	Interface   []*InterfaceEntry
}

func (t *testDBEntries) GetCreateOperations(c client.Client) []ovsdb.Operation {
	var operations []ovsdb.Operation

	var mdls []model.Model
	for _, o := range t.OpenVSwitch {
		mdls = append(mdls, o)
	}
	for _, o := range t.Bridge {
		mdls = append(mdls, o)
	}
	for _, o := range t.Port {
		mdls = append(mdls, o)
	}
	for _, o := range t.Interface {
		mdls = append(mdls, o)
	}
	for _, e := range mdls {
		if e != nil {
			o, err := c.Create(e)
			Expect(err).NotTo(HaveOccurred())
			operations = append(operations, o...)
		}
	}
	return operations
}

func getDefaultInitialDBContent() *testDBEntries {
	iface := &InterfaceEntry{
		Name:        "enp216s0f0np0",
		UUID:        uuid.NewString(),
		Type:        "dpdk",
		ExternalIDs: map[string]string{"iface_externalID_key": "iface_externalID_value"},
		OtherConfig: map[string]string{"iface_otherConfig_key": "iface_otherConfig_value"},
		Options:     map[string]string{"iface_options_key": "iface_options_value"},
	}
	port := &PortEntry{
		Name:       "enp216s0f0np0",
		UUID:       uuid.NewString(),
		Interfaces: []string{iface.UUID},
	}
	br := &BridgeEntry{
		Name:         "br-0000_d8_00.0",
		UUID:         uuid.NewString(),
		Ports:        []string{port.UUID},
		DatapathType: "netdev",
		ExternalIDs:  map[string]string{"br_externalID_key": "br_externalID_value"},
		OtherConfig:  map[string]string{"br_otherConfig_key": "br_otherConfig_value"},
	}
	ovs := &OpenvSwitchEntry{
		UUID:    uuid.NewString(),
		Bridges: []string{br.UUID},
	}
	return &testDBEntries{
		OpenVSwitch: []*OpenvSwitchEntry{ovs},
		Bridge:      []*BridgeEntry{br},
		Port:        []*PortEntry{port},
		Interface:   []*InterfaceEntry{iface},
	}
}

func getDBContent(ctx context.Context, c client.Client) *testDBEntries {
	ret := &testDBEntries{}
	Expect(c.List(ctx, &ret.OpenVSwitch)).NotTo(HaveOccurred())
	Expect(c.List(ctx, &ret.Bridge)).NotTo(HaveOccurred())
	Expect(c.List(ctx, &ret.Port)).NotTo(HaveOccurred())
	Expect(c.List(ctx, &ret.Interface)).NotTo(HaveOccurred())
	return ret
}

func createInitialDBContent(ctx context.Context, c client.Client, expectedState *testDBEntries) {
	operations := expectedState.GetCreateOperations(c)
	result, err := c.Transact(ctx, operations...)
	Expect(err).NotTo(HaveOccurred())
	operationsErr, err := ovsdb.CheckOperationResults(result, operations)
	Expect(err).NotTo(HaveOccurred())
	Expect(operationsErr).To(BeEmpty())
}

func validateDBConfig(dbContent *testDBEntries, conf *sriovnetworkv1.OVSConfigExt) {
	Expect(dbContent.OpenVSwitch).To(HaveLen(1))
	Expect(dbContent.Bridge).To(HaveLen(1))
	Expect(dbContent.Interface).To(HaveLen(1))
	Expect(dbContent.Port).To(HaveLen(1))
	ovs := dbContent.OpenVSwitch[0]
	br := dbContent.Bridge[0]
	port := dbContent.Port[0]
	iface := dbContent.Interface[0]
	Expect(ovs.Bridges).To(ContainElement(br.UUID))
	Expect(br.Name).To(Equal(conf.Name))
	Expect(br.DatapathType).To(Equal(conf.Bridge.DatapathType))
	Expect(br.OtherConfig).To(Equal(conf.Bridge.OtherConfig))
	Expect(br.ExternalIDs).To(Equal(conf.Bridge.ExternalIDs))
	Expect(br.Ports).To(ContainElement(port.UUID))
	Expect(port.Name).To(Equal(conf.Uplinks[0].Name))
	Expect(port.Interfaces).To(ContainElement(iface.UUID))
	Expect(iface.Name).To(Equal(conf.Uplinks[0].Name))
	Expect(iface.Options).To(Equal(conf.Uplinks[0].Interface.Options))
	Expect(iface.Type).To(Equal(conf.Uplinks[0].Interface.Type))
	Expect(iface.OtherConfig).To(Equal(conf.Uplinks[0].Interface.OtherConfig))
	Expect(iface.ExternalIDs).To(Equal(conf.Uplinks[0].Interface.ExternalIDs))
}

var _ = Describe("OVS", func() {
	var (
		ctx context.Context
	)
	BeforeEach(func() {
		ctx = context.Background()
	})
	Context("setDefaultTimeout", func() {
		It("use default", func() {
			newCtx, cFunc := setDefaultTimeout(ctx)
			deadline, isSet := newCtx.Deadline()
			Expect(isSet).To(BeTrue())
			Expect(time.Now().Before(deadline))
			Expect(cFunc).NotTo(BeNil())
			// cFunc should cancel the context
			cFunc()
			Expect(newCtx.Err()).To(MatchError(context.Canceled))

		})
		It("use explicit timeout - use configured timeout", func() {
			timeoutCtx, timeoutFunc := context.WithTimeout(ctx, time.Millisecond*100)
			defer timeoutFunc()
			newCtx, _ := setDefaultTimeout(timeoutCtx)
			time.Sleep(time.Millisecond * 200)
			Expect(newCtx.Err()).To(MatchError(context.DeadlineExceeded))
		})
		It("use explicit timeout - should return noop cancel function", func() {
			timeoutCtx, timeoutFunc := context.WithTimeout(ctx, time.Minute)
			defer timeoutFunc()
			newCtx, cFunc := setDefaultTimeout(timeoutCtx)
			Expect(cFunc).NotTo(BeNil())
			cFunc()
			Expect(newCtx.Err()).NotTo(HaveOccurred())
		})
	})

	Context("updateMap", func() {
		It("nil maps", func() {
			Expect(updateMap(nil, nil)).To(BeEmpty())
		})
		It("empty new map", func() {
			Expect(updateMap(map[string]string{"key": "val"}, nil)).To(BeEmpty())
		})
		It("empty old map", func() {
			Expect(updateMap(nil, map[string]string{"key": "val"})).To(BeEmpty())
		})
		It("update known values", func() {
			Expect(updateMap(
				map[string]string{"key2": "val2", "key4": "val4"},
				map[string]string{"key1": "val1new", "key2": "val2new", "key3": "val3new"})).To(
				Equal(
					map[string]string{"key2": "val2new"},
				))
		})
	})

	Context("manage bridges", func() {
		var (
			store            *ovsStoreMockPkg.MockStore
			testCtrl         *gomock.Controller
			tempDir          string
			testServerSocket string
			err              error
			stopServerFunc   func()
			ovsClient        client.Client
			ovs              Interface
		)
		BeforeEach(func() {
			tempDir, err = os.MkdirTemp("", "sriov-operator-ovs-test-dir*")
			testServerSocket = filepath.Join(tempDir, "ovsdb.sock")
			Expect(err).NotTo(HaveOccurred())
			testCtrl = gomock.NewController(GinkgoT())
			store = ovsStoreMockPkg.NewMockStore(testCtrl)
			_ = store
			stopServerFunc = startServer("unix", testServerSocket)

			origSocketValue := vars.OVSDBSocketPath
			vars.OVSDBSocketPath = "unix://" + testServerSocket
			DeferCleanup(func() {
				vars.OVSDBSocketPath = origSocketValue
			})

			ovsClient, err = getClient(ctx)
			Expect(err).NotTo(HaveOccurred())
			ovs = New(store)
		})

		AfterEach(func() {
			ovsClient.Close()
			stopServerFunc()
			Expect(os.RemoveAll(tempDir)).NotTo(HaveOccurred())
			testCtrl.Finish()
		})

		Context("CreateOVSBridge", func() {
			It("Bridge already exist with the right config, do nothing", func() {
				expectedConf := getManagedBridges()["br-0000_d8_00.0"]
				store.EXPECT().GetManagedOVSBridge("br-0000_d8_00.0").Return(expectedConf, nil)
				initialDBContent := getDefaultInitialDBContent()
				createInitialDBContent(ctx, ovsClient, initialDBContent)
				Expect(ovs.CreateOVSBridge(ctx, expectedConf)).NotTo(HaveOccurred())
				dbContent := getDBContent(ctx, ovsClient)
				// dbContent should be exactly same
				Expect(dbContent).To(Equal(initialDBContent))
			})
			It("No Bridge, create bridge", func() {
				expectedConf := getManagedBridges()["br-0000_d8_00.0"]
				store.EXPECT().GetManagedOVSBridge("br-0000_d8_00.0").Return(nil, nil)

				rootUUID := uuid.NewString()
				initialDBContent := &testDBEntries{OpenVSwitch: []*OpenvSwitchEntry{{UUID: rootUUID}}}

				createInitialDBContent(ctx, ovsClient, initialDBContent)

				store.EXPECT().AddManagedOVSBridge(expectedConf).Return(nil)
				Expect(ovs.CreateOVSBridge(ctx, expectedConf)).NotTo(HaveOccurred())

				validateDBConfig(getDBContent(ctx, ovsClient), expectedConf)
			})
			It("Bridge exist, no data in store, should recreate", func() {
				expectedConf := getManagedBridges()["br-0000_d8_00.0"]
				store.EXPECT().GetManagedOVSBridge("br-0000_d8_00.0").Return(nil, nil)
				store.EXPECT().AddManagedOVSBridge(expectedConf).Return(nil)
				initialDBContent := getDefaultInitialDBContent()
				createInitialDBContent(ctx, ovsClient, initialDBContent)

				Expect(ovs.CreateOVSBridge(ctx, expectedConf)).NotTo(HaveOccurred())

				dbContent := getDBContent(ctx, ovsClient)

				validateDBConfig(dbContent, expectedConf)
				// should recreate all objects
				Expect(dbContent.Bridge[0].UUID).NotTo(Equal(initialDBContent.Bridge[0].UUID))
				Expect(dbContent.Interface[0].UUID).NotTo(Equal(initialDBContent.Interface[0].UUID))
				Expect(dbContent.Port[0].UUID).NotTo(Equal(initialDBContent.Port[0].UUID))
			})
			It("Bridge exist with wrong config, should recreate", func() {
				expectedConf := getManagedBridges()["br-0000_d8_00.0"]
				expectedConf.Bridge.DatapathType = "test"

				oldConfig := getManagedBridges()["br-0000_d8_00.0"]
				store.EXPECT().GetManagedOVSBridge("br-0000_d8_00.0").Return(oldConfig, nil)
				store.EXPECT().AddManagedOVSBridge(expectedConf).Return(nil)

				initialDBContent := getDefaultInitialDBContent()
				createInitialDBContent(ctx, ovsClient, initialDBContent)

				Expect(ovs.CreateOVSBridge(ctx, expectedConf)).NotTo(HaveOccurred())

				dbContent := getDBContent(ctx, ovsClient)
				validateDBConfig(dbContent, expectedConf)

				Expect(dbContent.Bridge[0].UUID).NotTo(Equal(initialDBContent.Bridge[0].UUID))
			})
			It("Bridge exist with right config, interface has wrong config, should recreate interface only", func() {
				expectedConf := getManagedBridges()["br-0000_d8_00.0"]
				expectedConf.Uplinks[0].Interface.Type = "test"

				oldConfig := getManagedBridges()["br-0000_d8_00.0"]
				store.EXPECT().GetManagedOVSBridge("br-0000_d8_00.0").Return(oldConfig, nil)
				store.EXPECT().AddManagedOVSBridge(expectedConf).Return(nil)

				initialDBContent := getDefaultInitialDBContent()
				createInitialDBContent(ctx, ovsClient, initialDBContent)

				Expect(ovs.CreateOVSBridge(ctx, expectedConf)).NotTo(HaveOccurred())

				dbContent := getDBContent(ctx, ovsClient)
				validateDBConfig(dbContent, expectedConf)

				Expect(dbContent.Bridge[0].UUID).To(Equal(initialDBContent.Bridge[0].UUID))
				Expect(dbContent.Interface[0].UUID).NotTo(Equal(initialDBContent.Interface[0].UUID))
			})
			It("Interface has an error, should recreate interface only", func() {
				expectedConf := getManagedBridges()["br-0000_d8_00.0"]
				store.EXPECT().GetManagedOVSBridge("br-0000_d8_00.0").Return(expectedConf, nil)

				initialDBContent := getDefaultInitialDBContent()
				errMsg := "test"
				initialDBContent.Interface[0].Error = &errMsg
				createInitialDBContent(ctx, ovsClient, initialDBContent)

				Expect(ovs.CreateOVSBridge(ctx, expectedConf)).NotTo(HaveOccurred())

				dbContent := getDBContent(ctx, ovsClient)
				validateDBConfig(dbContent, expectedConf)

				// keep bridge, recreate iface
				Expect(dbContent.Bridge[0].UUID).To(Equal(initialDBContent.Bridge[0].UUID))
				Expect(dbContent.Interface[0].UUID).NotTo(Equal(initialDBContent.Interface[0].UUID))
			})
		})
		Context("GetOVSBridges", func() {
			It("Bridge exist, but no managed bridges in config", func() {
				createInitialDBContent(ctx, ovsClient, getDefaultInitialDBContent())
				store.EXPECT().GetManagedOVSBridges().Return(nil, nil)
				ret, err := ovs.GetOVSBridges(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(ret).To(BeEmpty())
			})
			It("Managed bridge exist with the right config", func() {
				createInitialDBContent(ctx, ovsClient, getDefaultInitialDBContent())
				conf := getManagedBridges()
				store.EXPECT().GetManagedOVSBridges().Return(conf, nil)
				ret, err := ovs.GetOVSBridges(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(ret).To(ContainElement(*conf["br-0000_d8_00.0"]))
			})
			It("Managed bridge exist, interface not found", func() {
				initialDBContent := getDefaultInitialDBContent()
				initialDBContent.Bridge[0].Ports = nil
				initialDBContent.Interface = nil
				initialDBContent.Port = nil
				createInitialDBContent(ctx, ovsClient, initialDBContent)
				conf := getManagedBridges()
				store.EXPECT().GetManagedOVSBridges().Return(conf, nil)
				ret, err := ovs.GetOVSBridges(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(ret).To(HaveLen(1))
				Expect(ret[0].Bridge).To(Equal(conf["br-0000_d8_00.0"].Bridge))
				Expect(ret[0].Uplinks).To(BeEmpty())
			})
			It("Config exist, bridge not found", func() {
				store.EXPECT().GetManagedOVSBridges().Return(getManagedBridges(), nil)
				ret, err := ovs.GetOVSBridges(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(ret).To(BeEmpty())
			})
			It("Should report only managed fields", func() {
				conf := getManagedBridges()
				store.EXPECT().GetManagedOVSBridges().Return(conf, nil)
				initialDBContent := getDefaultInitialDBContent()
				initialDBContent.Bridge[0].ExternalIDs["foo"] = "bar"
				initialDBContent.Bridge[0].OtherConfig["foo"] = "bar"
				initialDBContent.Interface[0].ExternalIDs["foo"] = "bar"
				initialDBContent.Interface[0].OtherConfig["foo"] = "bar"
				initialDBContent.Interface[0].Options["foo"] = "bar"
				createInitialDBContent(ctx, ovsClient, initialDBContent)
				ret, err := ovs.GetOVSBridges(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(ret).To(ContainElement(*conf["br-0000_d8_00.0"]))
			})
			It("Should not report managed fields which are missing in ovsdb", func() {
				initialDBContent := getDefaultInitialDBContent()
				initialDBContent.Bridge[0].ExternalIDs = nil
				initialDBContent.Bridge[0].OtherConfig = nil
				createInitialDBContent(ctx, ovsClient, initialDBContent)
				conf := getManagedBridges()
				store.EXPECT().GetManagedOVSBridges().Return(conf, nil)
				ret, err := ovs.GetOVSBridges(ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(ret).To(HaveLen(1))
				Expect(ret[0].Bridge.ExternalIDs).To(BeEmpty())
				Expect(ret[0].Bridge.OtherConfig).To(BeEmpty())
			})
		})
		Context("RemoveOVSBridge", func() {
			It("No config", func() {
				store.EXPECT().GetManagedOVSBridge("br-0000_d8_00.0").Return(nil, nil)
				Expect(ovs.RemoveOVSBridge(ctx, "br-0000_d8_00.0")).NotTo(HaveOccurred())
			})
			It("Has config, no bridge", func() {
				store.EXPECT().GetManagedOVSBridge("br-0000_d8_00.0").Return(getManagedBridges()["br-0000_d8_00.0"], nil)
				store.EXPECT().RemoveManagedOVSBridge("br-0000_d8_00.0").Return(nil)
				Expect(ovs.RemoveOVSBridge(ctx, "br-0000_d8_00.0")).NotTo(HaveOccurred())
			})
			It("Remove bridge", func() {
				store.EXPECT().GetManagedOVSBridge("br-0000_d8_00.0").Return(getManagedBridges()["br-0000_d8_00.0"], nil)
				store.EXPECT().RemoveManagedOVSBridge("br-0000_d8_00.0").Return(nil)
				createInitialDBContent(ctx, ovsClient, getDefaultInitialDBContent())
				Expect(ovs.RemoveOVSBridge(ctx, "br-0000_d8_00.0")).NotTo(HaveOccurred())
				dbContent := getDBContent(ctx, ovsClient)
				Expect(dbContent.Bridge).To(BeEmpty())
				Expect(dbContent.Interface).To(BeEmpty())
				Expect(dbContent.Port).To(BeEmpty())
			})
			It("Should keep unmanaged bridge", func() {
				store.EXPECT().GetManagedOVSBridge("br-0000_d8_00.0").Return(nil, nil)
				initialDBContent := getDefaultInitialDBContent()
				createInitialDBContent(ctx, ovsClient, initialDBContent)
				Expect(ovs.RemoveOVSBridge(ctx, "br-0000_d8_00.0")).NotTo(HaveOccurred())
				Expect(getDBContent(ctx, ovsClient)).To(Equal(initialDBContent))
			})
		})
		Context("RemoveInterfaceFromOVSBridge", func() {
			It("should not remove if interface is part of unmanaged bridge", func() {
				store.EXPECT().GetManagedOVSBridges().Return(nil, nil)
				initialDBContent := getDefaultInitialDBContent()
				createInitialDBContent(ctx, ovsClient, initialDBContent)
				Expect(ovs.RemoveInterfaceFromOVSBridge(ctx, "0000:d8:00.0")).NotTo(HaveOccurred())
				Expect(getDBContent(ctx, ovsClient)).To(Equal(initialDBContent))
			})
			It("should remove interface from managed bridge", func() {
				store.EXPECT().GetManagedOVSBridges().Return(getManagedBridges(), nil)
				initialDBContent := getDefaultInitialDBContent()
				createInitialDBContent(ctx, ovsClient, initialDBContent)
				Expect(ovs.RemoveInterfaceFromOVSBridge(ctx, "0000:d8:00.0")).NotTo(HaveOccurred())
				dbContent := getDBContent(ctx, ovsClient)
				Expect(dbContent.Bridge[0].UUID).To(Equal(initialDBContent.Bridge[0].UUID))
				Expect(dbContent.Interface).To(BeEmpty())
				Expect(dbContent.Port).To(BeEmpty())
			})
			It("bridge not found", func() {
				store.EXPECT().GetManagedOVSBridges().Return(getManagedBridges(), nil)
				store.EXPECT().RemoveManagedOVSBridge("br-0000_d8_00.0").Return(nil)
				Expect(ovs.RemoveInterfaceFromOVSBridge(ctx, "0000:d8:00.0")).NotTo(HaveOccurred())
			})
		})
	})

})

func startServer(protocol, path string) func() {
	clientDBModels, err := DatabaseModel()
	Expect(err).NotTo(HaveOccurred())
	schema := getSchema()
	ovsDB := inmemory.NewDatabase(map[string]model.ClientDBModel{
		schema.Name: clientDBModels,
	})

	dbModel, errs := model.NewDatabaseModel(schema, clientDBModels)
	Expect(errs).To(BeEmpty())
	s, err := server.NewOvsdbServer(ovsDB, dbModel)
	Expect(err).NotTo(HaveOccurred())

	stopped := make(chan struct{})

	go func() {
		defer GinkgoRecover()
		defer close(stopped)
		Expect(s.Serve(protocol, path)).NotTo(HaveOccurred())
	}()
	Eventually(func(g Gomega) {
		g.Expect(s.Ready()).To(BeTrue())
	}).WithTimeout(time.Second * 5).WithPolling(time.Millisecond * 100).Should(Succeed())
	return func() {
		s.Close()
		select {
		case <-stopped:
			return
		case <-time.After(time.Second * 10):
			Expect(fmt.Errorf("failed to stop ovsdb server")).NotTo(HaveOccurred())
		}
	}
}

const (
	schemaFile = "test_db.ovsschema"
)

// getSchema returns partial OVS schema to use in test OVSDB server
func getSchema() ovsdb.DatabaseSchema {
	schema, err := os.ReadFile(schemaFile)
	Expect(err).NotTo(HaveOccurred())
	var s ovsdb.DatabaseSchema
	err = json.Unmarshal(schema, &s)
	Expect(err).NotTo(HaveOccurred())
	return s
}
