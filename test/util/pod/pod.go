package pod

import (
	"bytes"
	"io"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/pointer"

	testclient "github.com/openshift/sriov-network-operator/test/util/client"
	"github.com/openshift/sriov-network-operator/test/util/images"
	"github.com/openshift/sriov-network-operator/test/util/namespaces"
)

const hostnameLabel = "kubernetes.io/hostname"

func getDefinition() *corev1.Pod {
	podObject := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "testpod-",
			Namespace:    namespaces.Test},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: pointer.Int64Ptr(0),
			Containers: []corev1.Container{{Name: "test",
				Image:   images.Test(),
				Command: []string{"/bin/bash", "-c", "sleep INF"}}}}}

	return podObject
}

func DefineWithNetworks(networks []string) *corev1.Pod {
	podObject := getDefinition()
	podObject.Annotations = map[string]string{"k8s.v1.cni.cncf.io/networks": strings.Join(networks, ",")}

	return podObject
}

func DefineWithHostNetwork(nodeName string) *corev1.Pod {
	podObject := getDefinition()
	podObject.Spec.HostNetwork = true
	podObject.Spec.NodeSelector = map[string]string{
		"kubernetes.io/hostname": nodeName,
	}

	return podObject
}

// RedefineAsPrivileged uppdates the pod to be privileged
func RedefineAsPrivileged(pod *corev1.Pod) *corev1.Pod {
	pod.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{}
	b := true
	pod.Spec.Containers[0].SecurityContext.Privileged = &b
	return pod
}

// RedefineWithHostNetwork uppdates the pod definition Spec.HostNetwork to true
func RedefineWithHostNetwork(pod *corev1.Pod) *corev1.Pod {
	pod.Spec.HostNetwork = true
	return pod
}

// RedefineWithNodeSelector uppdates the pod definition with a node selector
func RedefineWithNodeSelector(pod *corev1.Pod, node string) *corev1.Pod {
	pod.Spec.NodeSelector = map[string]string{
		hostnameLabel: node,
	}
	return pod
}

// RedefineWithCommand updates the pod defintion with a different command
func RedefineWithCommand(pod *corev1.Pod, command []string, args []string) *corev1.Pod {
	pod.Spec.Containers[0].Command = command
	pod.Spec.Containers[0].Args = args
	return pod
}

// RedefineWithRestartPolicy updates the pod defintion with a restart policy
func RedefineWithRestartPolicy(pod *corev1.Pod, restartPolicy corev1.RestartPolicy) *corev1.Pod {
	pod.Spec.RestartPolicy = restartPolicy
	return pod
}

// ExecCommand runs command in the pod and returns buffer output
func ExecCommand(cs *testclient.ClientSet, pod *corev1.Pod, command ...string) (string, string, error) {
	var buf, errbuf bytes.Buffer
	req := cs.CoreV1Interface.RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: pod.Spec.Containers[0].Name,
			Command:   command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cs.Config, "POST", req.URL())
	if err != nil {
		return buf.String(), errbuf.String(), err
	}

	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  os.Stdin,
		Stdout: &buf,
		Stderr: &errbuf,
		Tty:    true,
	})
	if err != nil {
		return buf.String(), errbuf.String(), err
	}

	return buf.String(), errbuf.String(), nil
}

// GetLog connects to a pod and fetches log
func GetLog(cs *testclient.ClientSet, p *corev1.Pod) (string, error) {
	req := cs.Pods(p.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{})
	log, err := req.Stream()
	if err != nil {
		return "", err
	}
	defer log.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, log)

	if err != nil {
		return "", err
	}

	return buf.String(), nil
}
