package tests

import (
	"os"
	"testing"

	"golangproxy/ssl"
)

func TestEnsureCertFiles(t *testing.T) {
	os.RemoveAll("ssl")
	err := ssl.EnsureCertFiles("ssl/cert.pem", "ssl/key.pem")
	if err != nil {
		t.Fatalf("Error generating certs: %v", err)
	}
	if _, err := os.Stat("ssl/cert.pem"); os.IsNotExist(err) {
		t.Error("Certificate file not created")
	}
}
