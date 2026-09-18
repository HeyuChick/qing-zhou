// Package certcheck validates a newly issued certificate before the panel
// stores or distributes it. It intentionally has no dependency on the store or
// API packages so every certificate issuance path can use the same gate.
package certcheck

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// Result is safe metadata derived from a verified certificate. It contains no
// certificate or private-key bytes.
type Result struct {
	KeyType    string
	Chain      string
	VerifiedAt int64
}

// Verify checks the key pair, requested hostname, validity window, and trust
// chain against the host's system roots. Callers must complete this check before
// replacing a certificate that is already serving live nodes.
func Verify(certPEM, keyPEM, domain string, now time.Time) (*Result, error) {
	return verifyWithRoots(certPEM, keyPEM, domain, now, nil)
}

func verifyWithRoots(certPEM, keyPEM, domain string, now time.Time, roots *x509.CertPool) (*Result, error) {
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return nil, fmt.Errorf("证书与私钥无效或不匹配：%w", err)
	}
	certs, err := parseCertificates(certPEM)
	if err != nil {
		return nil, err
	}
	leaf := certs[0]
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil, fmt.Errorf("证书核验缺少域名")
	}

	verifyName := domain
	if strings.HasPrefix(domain, "*.") {
		// VerifyHostname expects a concrete hostname, not a wildcard pattern.
		// ACME wildcard requests must instead contain that exact SAN.
		verifyName = ""
		found := false
		for _, san := range leaf.DNSNames {
			if strings.EqualFold(strings.TrimSpace(san), domain) {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("证书 SAN 不包含申请的泛域名 %s", domain)
		}
	}

	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	chains, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       verifyName,
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return nil, fmt.Errorf("证书域名、有效期或信任链核验失败：%w", err)
	}
	chain := certs
	if len(chains) > 0 {
		chain = chains[0]
	}
	return &Result{
		KeyType:    publicKeyType(leaf),
		Chain:      chainLabel(chain),
		VerifiedAt: now.Unix(),
	}, nil
}

func parseCertificates(certPEM string) ([]*x509.Certificate, error) {
	rest := []byte(certPEM)
	var certs []*x509.Certificate
	for len(rest) > 0 {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("证书 PEM 解析失败：%w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("证书 PEM 中没有 CERTIFICATE 块")
	}
	return certs, nil
}

func publicKeyType(cert *x509.Certificate) string {
	switch key := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA-%d", key.N.BitLen())
	case *ecdsa.PublicKey:
		if key.Curve != nil && key.Curve.Params() != nil {
			return "ECDSA-" + key.Curve.Params().Name
		}
		return "ECDSA"
	default:
		return cert.PublicKeyAlgorithm.String()
	}
}

func chainLabel(chain []*x509.Certificate) string {
	names := make([]string, 0, len(chain))
	for _, cert := range chain {
		name := strings.TrimSpace(cert.Subject.CommonName)
		if name == "" {
			name = strings.TrimSpace(cert.Subject.String())
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, " → ")
}
