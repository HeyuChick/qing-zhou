package singbox

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// GenerateSelfSignedCert issues a self-signed ECDSA (P-256) certificate for the
// given server name and returns the certificate and private key as PEM strings.
//
// It is intended for the "证书 TLS" mode with protocols whose clients accept a
// self-signed cert (TUIC / Hysteria2 with insecure or certificate pinning). The
// SAN is set from serverName: an IP literal becomes an IP SAN, otherwise a DNS
// SAN (plus its bare apex when serverName is a wildcard). validityDays is
// clamped to [1, 3650]; 0 means the default of 3650 (≈10 years).
func GenerateSelfSignedCert(serverName string, validityDays int) (certPEM, keyPEM string, err error) {
	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		return "", "", fmt.Errorf("server name is required")
	}
	if validityDays <= 0 {
		validityDays = 3650
	}
	if validityDays > 3650 {
		validityDays = 3650
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate key: %w", err)
	}

	// Serial: a random 128-bit positive integer.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", fmt.Errorf("generate serial: %w", err)
	}

	notBefore := time.Now().Add(-1 * time.Hour) // tolerate minor clock skew
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: serverName},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(time.Duration(validityDays) * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(serverName); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{serverName}
		// For a wildcard SNI, also cover the bare apex so both match.
		if strings.HasPrefix(serverName, "*.") {
			tmpl.DNSNames = append(tmpl.DNSNames, strings.TrimPrefix(serverName, "*."))
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("marshal key: %w", err)
	}

	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM, nil
}
