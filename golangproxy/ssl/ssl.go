package ssl

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"golangproxy/logger"
)

// EnsureCertFiles ensures SSL certificate and key files exist, generating self-signed if needed
func EnsureCertFiles(certPath, keyPath string) error {
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	if os.IsNotExist(certErr) || os.IsNotExist(keyErr) {
		logger.Logger.Printf("Certificate or key missing, generating new ones: %s, %s", certPath, keyPath)
		return generateSelfSignedCert(certPath, keyPath)
	}
	logger.Logger.Printf("Certificate and key found: %s, %s", certPath, keyPath)
	return nil
}

// generateSelfSignedCert creates a self-signed certificate and key
func generateSelfSignedCert(certPath, keyPath string) error {
	// Ensure ssl directory exists
	if err := os.MkdirAll(filepath.Dir(certPath), 0755); err != nil {
		logger.Logger.Printf("Error creating ssl directory: %v", err)
		return err
	}
	logger.Logger.Println("Created ssl directory")

	// Generate private key
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		logger.Logger.Printf("Error generating private key: %v", err)
		return err
	}
	if err := priv.Validate(); err != nil {
		logger.Logger.Printf("Generated private key is invalid: %v", err)
		return err
	}
	logger.Logger.Println("Generated and validated 2048-bit RSA private key")

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"GoLangProxy Self-Signed"},
			CommonName:   "example.com",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"example.com", "localhost"}, // SANs required
	}
	logger.Logger.Printf("Created certificate template with CN=%s, DNSNames=%v", template.Subject.CommonName, template.DNSNames)

	// Generate certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		logger.Logger.Printf("Error creating certificate: %v", err)
		return err
	}
	logger.Logger.Printf("Generated certificate, DER length: %d", len(certDER))

	// Write certificate
	certOut, err := os.Create(certPath)
	if err != nil {
		logger.Logger.Printf("Error creating cert file %s: %v", certPath, err)
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		logger.Logger.Printf("Error encoding cert PEM: %v", err)
		return err
	}
	logger.Logger.Printf("Wrote certificate to %s", certPath)

	// Write private key
	keyOut, err := os.Create(keyPath)
	if err != nil {
		logger.Logger.Printf("Error creating key file %s: %v", keyPath, err)
		return err
	}
	defer keyOut.Close()
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}); err != nil {
		logger.Logger.Printf("Error encoding key PEM: %v", err)
		return err
	}
	logger.Logger.Printf("Wrote private key to %s", keyPath)

	return nil
}
