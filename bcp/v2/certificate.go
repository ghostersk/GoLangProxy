package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

func generateSelfSignedCert() error {
	if err := os.MkdirAll(certDir, 0755); err != nil {
		return fmt.Errorf("failed to create certificate directory %s: %v", certDir, err)
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %v", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour)
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Proxy Self-Signed"},
			CommonName:   "localhost",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "*.example.com"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %v", err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %v", certPath, err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		certOut.Close()
		return fmt.Errorf("failed to encode certificate: %v", err)
	}
	certOut.Close()

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %v", keyPath, err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}); err != nil {
		keyOut.Close()
		return fmt.Errorf("failed to encode private key: %v", err)
	}
	keyOut.Close()

	refreshLogger.Printf("Generated self-signed certificate in %s", certDir)
	return nil
}

func loadCertificate() error {
	certFile, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("failed to read certificate %s: %v", certPath, err)
	}
	keyFile, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("failed to read key %s: %v", keyPath, err)
	}

	newCert, err := tls.X509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %v", err)
	}

	configMux.Lock()
	cert = &newCert
	configMux.Unlock()
	return nil
}

func monitorCertificates() {
	var lastModTime time.Time
	for {
		certInfo, err := os.Stat(certPath)
		if err != nil {
			errorLogger.Printf("Error checking certificate: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		keyInfo, err := os.Stat(keyPath)
		if err != nil {
			errorLogger.Printf("Error checking key: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if certInfo.ModTime() != lastModTime || keyInfo.ModTime() != lastModTime {
			if err := loadCertificate(); err != nil {
				errorLogger.Printf("Error reloading certificate: %v", err)
			} else {
				refreshLogger.Println("Certificate reloaded successfully")
				lastModTime = certInfo.ModTime()
			}
		}
		time.Sleep(5 * time.Second)
	}
}
