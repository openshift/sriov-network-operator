package webhook

import (
	"crypto/tls"
	"sync"

	"github.com/golang/glog"
)

// Config contains the server (the webhook) cert and key.
type Config struct {
	CertFile string
	KeyFile  string
}

type tlsKeypairReloader struct {
	certMutex sync.RWMutex
	cert      *tls.Certificate
	certPath  string
	keyPath   string
}

func (keyPair *tlsKeypairReloader) Reload() error {
	newCert, err := tls.LoadX509KeyPair(keyPair.certPath, keyPair.keyPath)
	if err != nil {
		return err
	}
	glog.Infof("cetificate reloaded")
	keyPair.certMutex.Lock()
	defer keyPair.certMutex.Unlock()
	keyPair.cert = &newCert
	return nil
}

func (keyPair *tlsKeypairReloader) GetCertificateFunc() func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		keyPair.certMutex.RLock()
		defer keyPair.certMutex.RUnlock()
		return keyPair.cert, nil
	}
}

func NewTlsKeypairReloader(certPath, keyPath string) (*tlsKeypairReloader, error) {
	result := &tlsKeypairReloader{
		certPath: certPath,
		keyPath:  keyPath,
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	result.cert = &cert

	return result, nil
}