package v1_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"io/ioutil"
	"path/filepath"
	"testing"

	v1 "github.com/openshift/sriov-network-operator/api/v1"
)

var update = flag.Bool("updategolden", false, "update .golden files")

func init() {
	// when running go tests path is local to the file, overriding it.
	v1.MANIFESTS_PATH = "../../bindata/manifests/cni-config"
}

func TestRendering(t *testing.T) {
	testtable := []struct {
		tname   string
		network v1.SriovNetwork
	}{
		{
			tname: "simple",
			network: v1.SriovNetwork{
				Spec: v1.SriovNetworkSpec{
					NetworkNamespace: "testnamespace",
					ResourceName:     "testresource",
				},
			},
		},
		{
			tname: "chained",
			network: v1.SriovNetwork{
				Spec: v1.SriovNetworkSpec{
					NetworkNamespace: "testnamespace",
					ResourceName:     "testresource",
					MetaPluginsConfig: `
					{
						"type": "vrf",
						"vrfname": "blue"
					}
					`,
				},
			},
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			var b bytes.Buffer
			w := bufio.NewWriter(&b)
			rendered, err := tc.network.RenderNetAttDef()
			if err != nil {
				t.Fatal("failed rendering network attachment definition", err)
			}
			encoder := json.NewEncoder(w)
			encoder.SetIndent("", "  ")
			encoder.Encode(rendered)
			w.Flush()
			gp := filepath.Join("testdata", filepath.FromSlash(t.Name())+".golden")
			if *update {
				t.Log("update golden file")
				if err := ioutil.WriteFile(gp, b.Bytes(), 0644); err != nil {
					t.Fatalf("failed to update golden file: %s", err)
				}
			}
			g, err := ioutil.ReadFile(gp)
			if err != nil {
				t.Fatalf("failed reading .golden: %s", err)
			}
			t.Log(string(b.Bytes()))
			if !bytes.Equal(b.Bytes(), g) {
				t.Errorf("bytes do not match .golden file")
			}
		})
	}
}

func TestIBRendering(t *testing.T) {
	testtable := []struct {
		tname   string
		network v1.SriovIBNetwork
	}{
		{
			tname: "simpleib",
			network: v1.SriovIBNetwork{
				Spec: v1.SriovIBNetworkSpec{
					NetworkNamespace: "testnamespace",
					ResourceName:     "testresource",
					Capabilities:     "foo",
				},
			},
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			var b bytes.Buffer
			w := bufio.NewWriter(&b)
			rendered, err := tc.network.RenderNetAttDef()
			if err != nil {
				t.Fatal("failed rendering network attachment definition", err)
			}
			encoder := json.NewEncoder(w)
			encoder.SetIndent("", "  ")
			encoder.Encode(rendered)
			w.Flush()
			gp := filepath.Join("testdata", filepath.FromSlash(t.Name())+".golden")
			if *update {
				t.Log("update golden file")
				if err := ioutil.WriteFile(gp, b.Bytes(), 0644); err != nil {
					t.Fatalf("failed to update golden file: %s", err)
				}
			}
			g, err := ioutil.ReadFile(gp)
			if err != nil {
				t.Fatalf("failed reading .golden: %s", err)
			}
			t.Log(string(b.Bytes()))
			if !bytes.Equal(b.Bytes(), g) {
				t.Errorf("bytes do not match .golden file")
			}
		})
	}
}
