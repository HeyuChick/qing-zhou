package certcheck

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/singbox"
)

func trustedTestCert(t *testing.T, domain string) (string, string, *x509.CertPool) {
	t.Helper()
	certPEM, keyPEM, err := singbox.GenerateSelfSignedCert(domain, 30)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(certPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return certPEM, keyPEM, roots
}

func TestVerifyWithRoots(t *testing.T) {
	certPEM, keyPEM, roots := trustedTestCert(t, "node.example.com")
	result, err := verifyWithRoots(certPEM, keyPEM, "node.example.com", time.Now(), roots)
	if err != nil {
		t.Fatal(err)
	}
	if result.KeyType != "ECDSA-P-256" || !strings.Contains(result.Chain, "node.example.com") || result.VerifiedAt == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestVerifyRejectsWrongHostnameAndKey(t *testing.T) {
	certPEM, keyPEM, roots := trustedTestCert(t, "node.example.com")
	if _, err := verifyWithRoots(certPEM, keyPEM, "other.example.com", time.Now(), roots); err == nil {
		t.Error("wrong hostname was accepted")
	}
	_, otherKey, _ := trustedTestCert(t, "node.example.com")
	if _, err := verifyWithRoots(certPEM, otherKey, "node.example.com", time.Now(), roots); err == nil {
		t.Error("wrong private key was accepted")
	}
}

func TestVerifyWildcardRequiresExactSAN(t *testing.T) {
	certPEM, keyPEM, roots := trustedTestCert(t, "*.example.com")
	if _, err := verifyWithRoots(certPEM, keyPEM, "*.example.com", time.Now(), roots); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyWithRoots(certPEM, keyPEM, "*.other.example.com", time.Now(), roots); err == nil {
		t.Error("different wildcard SAN was accepted")
	}
}
