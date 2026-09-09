package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/webhook"
)

var (
	certFile    string
	keyFile     string
	port        int
	enableHTTP2 bool
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Starts Webhook Daemon",
	Long:  "Starts Webhook Daemon",
	Run:   runStartCmd,
}

// admitv1Func handles a v1 admission
type admitv1Func func(v1.AdmissionReview) *v1.AdmissionResponse

// admitHandler is a handler, for both validators and mutators, that supports multiple admission review versions
type admitHandler struct {
	v1 admitv1Func
}

func init() {
	rootCmd.AddCommand(startCmd)

	startCmd.Flags().StringVar(&certFile, "tls-cert-file", "",
		"File containing the default x509 Certificate for HTTPS. (CA cert, if any, concatenated after server cert).")
	startCmd.Flags().StringVar(&keyFile, "tls-private-key-file", "",
		"File containing the default x509 private key matching --tls-cert-file.")
	startCmd.Flags().IntVar(&port, "port", 443,
		"Secure port that the webhook listens on")
	startCmd.Flags().BoolVar(&enableHTTP2, "enable-http2", false, "If HTTP/2 should be enabled for the metrics and webhook servers.")
}

// serve handles the http portion of a request prior to handing to an admit
// function
func serve(w http.ResponseWriter, r *http.Request, admit admitHandler) {
	serveLog := log.Log.WithName("serve")

	var body []byte
	if r.Body != nil {
		if data, err := io.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		serveLog.Error(fmt.Errorf("unexpected content type"),
			"expect Content-Type application/json", "Content-Type", contentType)
		return
	}

	serveLog.V(2).Info("handling request", "request-body", string(body))

	deserializer := webhook.Codecs.UniversalDeserializer()
	obj, gvk, err := deserializer.Decode(body, nil, nil)
	if err != nil {
		msg := fmt.Sprintf("Request could not be decoded: %v", err)
		serveLog.Error(nil, msg)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	var responseObj runtime.Object
	switch *gvk {
	case v1.SchemeGroupVersion.WithKind("AdmissionReview"):
		requestedAdmissionReview, ok := obj.(*v1.AdmissionReview)
		if !ok {
			err := fmt.Errorf("unexpected object")
			serveLog.Error(err, "Expected v1.AdmissionReview", "Actual-Type", reflect.TypeOf(obj).String())
			return
		}
		responseAdmissionReview := &v1.AdmissionReview{}
		responseAdmissionReview.SetGroupVersionKind(*gvk)
		responseAdmissionReview.Response = admit.v1(*requestedAdmissionReview)
		responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID
		responseObj = responseAdmissionReview
	default:
		msg := fmt.Sprintf("Unsupported group version kind: %v", gvk)
		serveLog.Error(nil, msg)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	respBytes, err := json.Marshal(responseObj)
	if err != nil {
		serveLog.Error(err, "failed to marshal response object")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	serveLog.V(2).Info("sending response", "response", string(respBytes[:]))
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(respBytes); err != nil {
		serveLog.Error(err, "failed to write response")
	}
}

func serveMutateCustomResource(w http.ResponseWriter, r *http.Request) {
	serve(w, r, newDelegateToV1AdmitHandler(webhook.MutateCustomResource))
}

func serveValidateCustomResource(w http.ResponseWriter, r *http.Request) {
	serve(w, r, newDelegateToV1AdmitHandler(webhook.ValidateCustomResource))
}

func newDelegateToV1AdmitHandler(f admitv1Func) admitHandler {
	return admitHandler{
		v1: f,
	}
}

func runStartCmd(cmd *cobra.Command, args []string) {
	// init logger
	snolog.InitLog()
	setupLog := log.Log.WithName("sriov-network-operator-webhook")

	setupLog.Info("Run sriov-network-operator-webhook")

	if err := webhook.SetupInClusterClient(); err != nil {
		setupLog.Error(err, "failed to setup in-cluster client")
		panic(err)
	}

	if err := webhook.RetriveSupportedNics(); err != nil {
		setupLog.Error(err, "failed to retrieve supported NICs")
		panic(err)
	}

	keyPair, err := webhook.NewTLSKeypairReloader(certFile, keyFile)
	if err != nil {
		setupLog.Error(err, "failed to load certificates", "cert-file", certFile, "key-file", keyFile)
		panic(err)
	}

	http.HandleFunc("/mutating-custom-resource", serveMutateCustomResource)
	http.HandleFunc("/validating-custom-resource", serveValidateCustomResource)
	http.HandleFunc("/readyz", func(w http.ResponseWriter, req *http.Request) { w.Write([]byte("ok")) })

	go func() {
		setupLog.Info("start server")
		server := &http.Server{
			Addr: fmt.Sprintf(":%d", port),
			TLSConfig: &tls.Config{
				GetCertificate: keyPair.GetCertificateFunc(),
			},
			// CVE-2023-39325 https://github.com/golang/go/issues/63417
			TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
		}
		if enableHTTP2 {
			server.TLSNextProto = nil
		}
		err := server.ListenAndServeTLS("", "")
		if err != nil {
			setupLog.Error(err, "failed to listen for requests")
			panic(err)
		}
	}()
	/* watch the cert file and restart http sever if the file updated. */
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		setupLog.Error(err, "error starting fsnotify watcher")
		panic(err)
	}
	defer watcher.Close()

	certUpdated := false
	keyUpdated := false

	watcher.Add(certFile)
	watcher.Add(keyFile)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				continue
			}
			setupLog.Info("watcher event", "event", event)
			mask := fsnotify.Create | fsnotify.Rename | fsnotify.Remove |
				fsnotify.Write | fsnotify.Chmod
			if (event.Op & mask) != 0 {
				setupLog.Info("modified file", "name", event.Name)
				if event.Name == certFile {
					certUpdated = true
					watcher.Add(certFile)
				}
				if event.Name == keyFile {
					keyUpdated = true
					watcher.Add(keyFile)
				}
				if keyUpdated && certUpdated {
					if err := keyPair.Reload(); err != nil {
						setupLog.Error(err, "failed to reload certificate")
						panic("failed to reload certificate")
					}
					certUpdated = false
					keyUpdated = false
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				continue
			}
			setupLog.Error(err, "watcher error")
		}
	}
}
