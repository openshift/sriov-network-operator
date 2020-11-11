package render

import (
	"testing"

	"github.com/openshift/machine-config-operator/pkg/controller/common"

	. "github.com/onsi/gomega"
)

// TestRenderSimple tests rendering a single object with no templates
func TestRenderSimple(t *testing.T) {
	g := NewGomegaWithT(t)

	d := MakeRenderData()

	o1, err := RenderTemplate("testdata/simple.yaml", &d)
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
	o2, err := RenderTemplate("testdata/simple.json", &d)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(o2).To(Equal(o1))
}

func TestRenderMultiple(t *testing.T) {
	g := NewGomegaWithT(t)

	p := "testdata/multiple.yaml"
	d := MakeRenderData()

	o, err := RenderTemplate(p, &d)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(o).To(HaveLen(3))

	g.Expect(o[0].GetObjectKind().GroupVersionKind().String()).To(Equal("/v1, Kind=Pod"))
	g.Expect(o[1].GetObjectKind().GroupVersionKind().String()).To(Equal("rbac.authorization.k8s.io/v1, Kind=ClusterRoleBinding"))
	g.Expect(o[2].GetObjectKind().GroupVersionKind().String()).To(Equal("/v1, Kind=ConfigMap"))
}

func TestTemplate(t *testing.T) {
	g := NewGomegaWithT(t)

	p := "testdata/template.yaml"

	// Test that missing variables are detected
	d := MakeRenderData()
	_, err := RenderTemplate(p, &d)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(HaveSuffix(`function "fname" not defined`))

	// Set expected function (but not variable)
	d.Funcs["fname"] = func(s string) string { return "test-" + s }
	_, err = RenderTemplate(p, &d)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(HaveSuffix(`has no entry for key "Namespace"`))

	// now we can render
	d.Data["Namespace"] = "myns"
	o, err := RenderTemplate(p, &d)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(o[0].GetName()).To(Equal("test-podname"))
	g.Expect(o[0].GetNamespace()).To(Equal("myns"))
	g.Expect(o[0].Object["foo"]).To(Equal("fallback"))
	g.Expect(o[0].Object["bar"]).To(Equal("myns"))
}

func TestRenderDir(t *testing.T) {
	g := NewGomegaWithT(t)

	d := MakeRenderData()
	d.Funcs["fname"] = func(s string) string { return s }
	d.Data["Namespace"] = "myns"

	o, err := RenderDir("testdata", &d)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(o).To(HaveLen(6))
}

func TestGenerateOffloadMachineConfig(t *testing.T) {
	g := NewGomegaWithT(t)

	d := MakeRenderData()
	d.Data["deviceList"] = []DeviceInfo{
		{"0000.18:00.0", 16},
		{"0000.18:00.1", 16},
	}
	expect := "data:,0000.18%3A00.0%2016%0A0000.18%3A00.1%2016%0A"

	mc, err := GenerateOffloadMachineConfig("testdata/machineconfig", "offload", &d)
	g.Expect(err).NotTo(HaveOccurred())
	err = common.ValidateMachineConfig(mc.Spec)
	g.Expect(err).NotTo(HaveOccurred())
	ign, err := common.ParseAndConvertConfig(mc.Spec.Config.Raw)
	// t.Errorf("config: %s", string(mc.Spec.Config.Raw))
	g.Expect(err).NotTo(HaveOccurred())
	err = common.ValidateIgnition(ign)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ign.Storage.Files).To(HaveLen(2))
	g.Expect(*(ign.Storage.Files[1].Contents.Source)).To(Equal(expect))
	g.Expect(ign.Systemd.Units).To(HaveLen(4))
}
