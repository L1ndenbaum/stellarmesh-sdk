package kafka

import (
	"crypto/tls"
	"strings"
	"testing"
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
		{name: "plain", config: ConnectionConfig{
			SecurityProtocol: SecurityProtocolSASLPlaintext, SASLMechanism: SASLMechanismPlain,
			Username: "user", Password: "secret",
		}, wantSASL: "PLAIN"},
		{name: "scram256", config: ConnectionConfig{
			SecurityProtocol: SecurityProtocolSASLTLS, SASLMechanism: SASLMechanismSCRAMSHA256,
			Username: "user", Password: "secret",
		}, wantTLS: true, wantSASL: "SCRAM-SHA-256"},
		{name: "scram512", config: ConnectionConfig{
			SecurityProtocol: SecurityProtocolSASLTLS, SASLMechanism: SASLMechanismSCRAMSHA512,
			Username: "user", Password: "secret",
		}, wantTLS: true, wantSASL: "SCRAM-SHA-512"},
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
	tests := []ConnectionConfig{
		{SecurityProtocol: "UNKNOWN"},
		{SecurityProtocol: SecurityProtocolPlaintext, Username: "user", Password: "secret"},
		{SecurityProtocol: SecurityProtocolTLS, TLSCertFile: "cert.pem"},
		{SecurityProtocol: SecurityProtocolSASLTLS, SASLMechanism: SASLMechanismPlain},
		{SecurityProtocol: SecurityProtocolSASLPlaintext, SASLMechanism: "UNKNOWN", Username: "user", Password: "secret"},
	}
	for _, config := range tests {
		if _, err := NewConnection(config); err == nil {
			t.Fatalf("NewConnection() accepted %#v", config)
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatalf("error leaked password: %v", err)
		}
	}
}
