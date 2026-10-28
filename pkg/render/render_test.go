package render

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

func TestRenderOvsVswitchdDropin(t *testing.T) {
	for _, tt := range []struct {
		name           string
		otherOvsConfig string
		externalIds    string
		commandCount   int
	}{
		{name: "empty", commandCount: 2},
		{name: "configured", otherOvsConfig: `"other_config:hw-offload=true"`, externalIds: "hw-offload", commandCount: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			d := MakeRenderData()
			d.Data["OtherOvsConfig"] = tt.otherOvsConfig
			d.Data["ExternalIds"] = tt.externalIds
			rendered, err := renderFileTemplate("../../bindata/manifests/switchdev-config/ovs-units/ovs-vswitchd.service.yaml", &d)
			g.Expect(err).NotTo(HaveOccurred())
			var manifest map[string]interface{}
			g.Expect(yaml.Unmarshal(rendered.Bytes(), &manifest)).To(Succeed())
			dropin := manifest["dropins"].([]interface{})[0].(map[string]interface{})
			contents := dropin["contents"].(string)
			g.Expect(strings.Count(contents, "ExecStartPre=")).To(Equal(tt.commandCount))
			g.Expect(contents).To(ContainSubstring(`external_ids:sriov-operator-owned-keys=` + tt.externalIds + `"`))
			g.Expect(contents).To(ContainSubstring("remove Open_vSwitch . other_config"))
			setCommand := "ExecStartPre=:/bin/ovs-vsctl --no-wait set Open_vSwitch . "
			if tt.otherOvsConfig == "" {
				g.Expect(contents).NotTo(ContainSubstring(setCommand))
			} else {
				g.Expect(contents).To(ContainSubstring(setCommand + tt.otherOvsConfig + "\n"))
			}
		})
	}
}

// TestRenderSimple tests rendering a single object with no templates
func TestRenderSimple(t *testing.T) {
	g := NewGomegaWithT(t)

	d := MakeRenderData()

	o1, err := RenderFileTemplate("testdata/manifests/simple.yaml", &d)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(o1).To(HaveLen(1))
	expected := `
{
	"apiVersion": "v1",
	"kind": "Pod",
	"metadata": {
		"name": "busybox1",
		"namespace": "ns"
	},
	"spec": {
		"containers": [
			{
  				"image": "busybox"
			}
		]
	}
}
`
	g.Expect(o1[0].MarshalJSON()).To(MatchJSON(expected))

	// test that json parses the same
	o2, err := RenderFileTemplate("testdata/manifests/simple.json", &d)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(o2).To(Equal(o1))
}

func TestRenderMultiple(t *testing.T) {
	g := NewGomegaWithT(t)

	p := "testdata/manifests/multiple.yaml"
	d := MakeRenderData()

	o, err := RenderFileTemplate(p, &d)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(o).To(HaveLen(3))

	g.Expect(o[0].GetObjectKind().GroupVersionKind().String()).To(Equal("/v1, Kind=Pod"))
	g.Expect(o[1].GetObjectKind().GroupVersionKind().String()).To(Equal("rbac.authorization.k8s.io/v1, Kind=ClusterRoleBinding"))
	g.Expect(o[2].GetObjectKind().GroupVersionKind().String()).To(Equal("/v1, Kind=ConfigMap"))
}

func TestTemplate(t *testing.T) {
	g := NewGomegaWithT(t)

	p := "testdata/manifests/template.yaml"

	// Test that missing variables are detected
	d := MakeRenderData()
	_, err := RenderFileTemplate(p, &d)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(HaveSuffix(`function "fname" not defined`))

	// Set expected function (but not variable)
	d.Funcs["fname"] = func(s string) string { return "test-" + s }
	_, err = RenderFileTemplate(p, &d)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(HaveSuffix(`has no entry for key "Namespace"`))

	// now we can render
	d.Data["Namespace"] = "myns"
	o, err := RenderFileTemplate(p, &d)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(o[0].GetName()).To(Equal("test-podname"))
	g.Expect(o[0].GetNamespace()).To(Equal("myns"))
	g.Expect(o[0].Object["foo"]).To(Equal("fallback"))
	g.Expect(o[0].Object["bar"]).To(Equal("myns"))
}

// TestTemplateWithEmptyObject tests the case where a file generates additional nil objects when rendered. An empty
// object can also occur in the particular case shown in the testfile below when minus is missing at the end of the
// first expression (i.e. {{- if .Enable }}).
func TestTemplateWithEmptyObject(t *testing.T) {
	g := NewGomegaWithT(t)

	p := "testdata/manifests/template_with_empty_object.yaml"

	d := MakeRenderData()
	d.Data["Enable"] = true
	o, err := RenderFileTemplate(p, &d)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(len(o)).To(Equal(2))
	g.Expect(o[0].GetName()).To(Equal("pod1"))
	g.Expect(o[0].GetNamespace()).To(Equal("namespace1"))
	g.Expect(o[1].GetName()).To(Equal("pod2"))
	g.Expect(o[1].GetNamespace()).To(Equal("namespace2"))
}

func TestRenderDir(t *testing.T) {
	g := NewGomegaWithT(t)

	d := MakeRenderData()
	d.Funcs["fname"] = func(s string) string { return s }
	d.Data["Namespace"] = "myns"
	d.Data["Enable"] = true

	o, err := RenderDir("testdata/manifests", &d)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(o).To(HaveLen(8))
}
