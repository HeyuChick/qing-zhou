package singbox

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"
)

func TestGenerateSelfSignedCert_DNS(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSignedCert("proxy.example.com", 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// The pair must load as a valid, matching keypair.
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("cert PEM did not decode")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if cert.Subject.CommonName != "proxy.example.com" {
		t.Errorf("CN = %q, want proxy.example.com", cert.Subject.CommonName)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "proxy.example.com" {
		t.Errorf("DNSNames = %v, want [proxy.example.com]", cert.DNSNames)
	}
	// Default validity ≈ 3650 days.
	if d := cert.NotAfter.Sub(cert.NotBefore); d < 3600*24*time.Hour {
		t.Errorf("validity too short: %v", d)
	}
	if err := cert.VerifyHostname("proxy.example.com"); err != nil {
		t.Errorf("VerifyHostname: %v", err)
	}
}

func TestGenerateSelfSignedCert_IP(t *testing.T) {
	certPEM, _, err := GenerateSelfSignedCert("203.0.113.7", 30)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	block, _ := pem.Decode([]byte(certPEM))
	cert, _ := x509.ParseCertificate(block.Bytes)
	if len(cert.IPAddresses) != 1 || !cert.IPAddresses[0].Equal(net.ParseIP("203.0.113.7")) {
		t.Errorf("IPAddresses = %v, want [203.0.113.7]", cert.IPAddresses)
	}
	if len(cert.DNSNames) != 0 {
		t.Errorf("DNSNames = %v, want empty for an IP SAN", cert.DNSNames)
	}
}

func TestGenerateSelfSignedCert_Empty(t *testing.T) {
	if _, _, err := GenerateSelfSignedCert("  ", 0); err == nil {
		t.Error("expected error for empty server name")
	}
}
