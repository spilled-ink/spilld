// Package devcert generates a local TLS server certificate using the mkcert root CA.
package devcert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Config creates a *tls.Config with a local CA serving certificate.
func Config() (*tls.Config, error) {
	certOnce.Do(createCert)

	if certErr != nil {
		return nil, certErr
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
	}, nil
}

var (
	cert     tls.Certificate
	certErr  error
	certOnce sync.Once
)

func createCert() {
	caCert, caKey, err := loadCA()
	if err != nil {
		if os.IsNotExist(err) {
			certErr = fmt.Errorf("devcert: no mkcert CA root found.\n\n" +
				"Install a development CA root by running:\n" +
				"\tgo get -u github.com/FiloSottile/mkcert\n" +
				"\tmkcert -install\n")
			return
		}
		certErr = fmt.Errorf("devcert: mkcert CA load failed: %v", err)
		return
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	//priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		certErr = fmt.Errorf("devcert: %v", err)
		return
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 64)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		certErr = fmt.Errorf("devcert: cannot generate a serial number: %v", err)
		return
	}

	tmpl := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization:       []string{"local spilld development certificate"},
			OrganizationalUnit: []string{"local spilld developer"},
		},
		NotAfter:    time.Now().AddDate(1, 1, 0),
		NotBefore:   time.Now(),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},

		BasicConstraintsValid: true,
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
		DNSNames: []string{"localhost"},
	}
	// TODO: include net.Interfaces() in IPAddresses
	if host, _ := os.Hostname(); host != "" {
		tmpl.DNSNames = append(tmpl.DNSNames, host)
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, priv.Public(), caKey)
	if err != nil {
		certErr = fmt.Errorf("devcert: %v", err)
		return
	}

	cert.Certificate = append(cert.Certificate, certBytes)
	cert.PrivateKey = priv
}

func readFilePEM(path string) ([]byte, error) {
	pemb, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	der, _ := pem.Decode(pemb)
	if der == nil {
		return nil, fmt.Errorf("could not PEM decode %s", path)
	}
	return der.Bytes, nil
}

func loadCA() (*x509.Certificate, crypto.PrivateKey, error) {
	dir := filepath.Join(osCertDir(), "mkcert")

	certBytes, err := readFilePEM(filepath.Join(dir, "rootCA.pem"))
	if err != nil {
		return nil, nil, err
	}
	caCert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, nil, err
	}

	keyBytes, err := readFilePEM(filepath.Join(dir, "rootCA-key.pem"))
	if err != nil {
		return nil, nil, err
	}
	caKey, err := x509.ParsePKCS8PrivateKey(keyBytes)
	if err != nil {
		return nil, nil, err
	}
	return caCert, caKey, nil
}

// osCertDir returns the directory that mkcert uses for storing its root CA key.
// This function needs to produce the same result as mkcert.
func osCertDir() string {
	switch {
	case runtime.GOOS == "windows":
		return os.Getenv("LocalAppData")
	case os.Getenv("XDG_DATA_HOME") != "":
		return os.Getenv("XDG_DATA_HOME")
	default:
		dir := os.Getenv("HOME")
		if dir == "" {
			return ""
		}
		if runtime.GOOS == "darwin" {
			return filepath.Join(dir, "Library", "Application Support")
		}
		return filepath.Join(dir, ".local", "share")
	}
}
