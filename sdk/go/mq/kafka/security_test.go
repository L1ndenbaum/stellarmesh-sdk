package kafka

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConnectionSupportsPlainTLSAndSASL(t *testing.T) {
	tests := []struct {
		name     string
		config   ConnectionConfig
		wantTLS  bool
		wantSASL string
	}{
		{name: "plaintext", config: ConnectionConfig{}},
		{name: "tls", config: ConnectionConfig{SecurityProtocol: SecurityProtocolTLS}, wantTLS: true},
	}
	for _, protocol := range []SecurityProtocol{SecurityProtocolSASLPlaintext, SecurityProtocolSASLTLS} {
		for _, mechanism := range []SASLMechanism{SASLMechanismPlain, SASLMechanismSCRAMSHA256, SASLMechanismSCRAMSHA512} {
			tests = append(tests, struct {
				name     string
				config   ConnectionConfig
				wantTLS  bool
				wantSASL string
			}{
				name: string(protocol) + "/" + string(mechanism),
				config: ConnectionConfig{
					SecurityProtocol: protocol,
					SASLMechanism:    mechanism,
					Username:         "user",
					Password:         "secret",
				},
				wantTLS:  protocol == SecurityProtocolSASLTLS,
				wantSASL: string(mechanism),
			})
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection, err := NewConnection(test.config)
			if err != nil {
				t.Fatal(err)
			}
			dialer := connection.Dialer()
			if (dialer.TLS != nil) != test.wantTLS {
				t.Fatalf("TLS configured = %t", dialer.TLS != nil)
			}
			if dialer.TLS != nil && dialer.TLS.MinVersion != tls.VersionTLS12 {
				t.Fatalf("TLS minimum = %d", dialer.TLS.MinVersion)
			}
			if test.wantSASL == "" && dialer.SASLMechanism != nil {
				t.Fatalf("unexpected SASL = %q", dialer.SASLMechanism.Name())
			}
			if test.wantSASL != "" && (dialer.SASLMechanism == nil || dialer.SASLMechanism.Name() != test.wantSASL) {
				t.Fatalf("SASL = %#v", dialer.SASLMechanism)
			}
		})
	}
}

func TestConnectionRejectsInvalidSecurityCombinations(t *testing.T) {
	tests := []struct {
		name   string
		config ConnectionConfig
	}{
		{name: "protocol", config: ConnectionConfig{SecurityProtocol: "UNKNOWN"}},
		{name: "credentials without SASL", config: ConnectionConfig{
			SecurityProtocol: SecurityProtocolPlaintext, Username: "user", Password: "sensitive-password",
		}},
		{name: "certificate without key", config: ConnectionConfig{
			SecurityProtocol: SecurityProtocolTLS, TLSCertFile: "cert.pem",
		}},
		{name: "key without certificate", config: ConnectionConfig{
			SecurityProtocol: SecurityProtocolTLS, TLSKeyFile: "key.pem",
		}},
		{name: "empty username", config: ConnectionConfig{
			SecurityProtocol: SecurityProtocolSASLTLS, SASLMechanism: SASLMechanismPlain, Password: "sensitive-password",
		}},
		{name: "empty password", config: ConnectionConfig{
			SecurityProtocol: SecurityProtocolSASLTLS, SASLMechanism: SASLMechanismPlain, Username: "user",
		}},
		{name: "mechanism", config: ConnectionConfig{
			SecurityProtocol: SecurityProtocolSASLPlaintext, SASLMechanism: "UNKNOWN",
			Username: "user", Password: "sensitive-password",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewConnection(test.config); err == nil {
				t.Fatalf("NewConnection() accepted %#v", test.config)
			} else if strings.Contains(err.Error(), "sensitive-password") {
				t.Fatalf("error leaked password: %v", err)
			}
		})
	}
}

func TestConnectionLoadsCAAndMutualTLSCertificate(t *testing.T) {
	certificatePath, keyPath := writeTestCertificate(t)
	connection, err := NewConnection(ConnectionConfig{
		SecurityProtocol: SecurityProtocolTLS,
		TLSCAFile:        certificatePath,
		TLSCertFile:      certificatePath,
		TLSKeyFile:       keyPath,
		TLSServerName:    "kafka.internal",
	})
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig := connection.Dialer().TLS
	if tlsConfig == nil || tlsConfig.RootCAs == nil {
		t.Fatal("TLS CA was not loaded")
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Fatalf("client certificate count = %d", len(tlsConfig.Certificates))
	}
	if tlsConfig.ServerName != "kafka.internal" || tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLS config = %#v", tlsConfig)
	}

	transportTLS := connection.Transport().TLS
	if transportTLS == tlsConfig || transportTLS.ServerName != tlsConfig.ServerName {
		t.Fatal("transport did not receive an independent TLS configuration")
	}
}

func TestConnectionRejectsCAWithoutCertificate(t *testing.T) {
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewConnection(ConnectionConfig{SecurityProtocol: SecurityProtocolTLS, TLSCAFile: caPath})
	if err == nil || !strings.Contains(err.Error(), "contains no certificates") {
		t.Fatalf("error = %v", err)
	}
}

func writeTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Kafka test CA"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	temporaryDirectory := t.TempDir()
	certificatePath := filepath.Join(temporaryDirectory, "client.pem")
	keyPath := filepath.Join(temporaryDirectory, "client-key.pem")
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certificatePath, keyPath
}
