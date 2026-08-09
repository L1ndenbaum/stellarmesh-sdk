package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	segmentio "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
)

// SecurityProtocol selects Kafka transport encryption and authentication.
type SecurityProtocol string

const (
	SecurityProtocolPlaintext     SecurityProtocol = "PLAINTEXT"
	SecurityProtocolTLS           SecurityProtocol = "TLS"
	SecurityProtocolSASLPlaintext SecurityProtocol = "SASL_PLAINTEXT"
	SecurityProtocolSASLTLS       SecurityProtocol = "SASL_TLS"
)

// SASLMechanism selects a supported Kafka SASL exchange.
type SASLMechanism string

const (
	SASLMechanismPlain       SASLMechanism = "PLAIN"
	SASLMechanismSCRAMSHA256 SASLMechanism = "SCRAM-SHA-256"
	SASLMechanismSCRAMSHA512 SASLMechanism = "SCRAM-SHA-512"
)

// ConnectionConfig contains reusable Kafka client security settings.
type ConnectionConfig struct {
	ClientID         string
	SecurityProtocol SecurityProtocol
	SASLMechanism    SASLMechanism
	Username         string
	Password         string
	TLSCAFile        string
	TLSCertFile      string
	TLSKeyFile       string
	TLSServerName    string
	DialTimeout      time.Duration
}

// Connection owns immutable TLS and SASL settings for Kafka clients.
type Connection struct {
	clientID    string
	dialTimeout time.Duration
	tlsConfig   *tls.Config
	mechanism   sasl.Mechanism
}

// NewConnection validates security settings and loads any referenced certificates.
func NewConnection(config ConnectionConfig) (*Connection, error) {
	protocol := config.SecurityProtocol
	if protocol == "" {
		protocol = SecurityProtocolPlaintext
	}
	dialTimeout := config.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 10 * time.Second
	}
	if dialTimeout < 0 {
		return nil, errors.New("Kafka dial timeout must not be negative")
	}

	useTLS := protocol == SecurityProtocolTLS || protocol == SecurityProtocolSASLTLS
	useSASL := protocol == SecurityProtocolSASLPlaintext || protocol == SecurityProtocolSASLTLS
	if !useTLS && !useSASL && protocol != SecurityProtocolPlaintext {
		return nil, fmt.Errorf("unsupported Kafka security protocol %q", protocol)
	}

	var tlsConfig *tls.Config
	var err error
	if useTLS {
		tlsConfig, err = loadTLSConfig(config)
		if err != nil {
			return nil, err
		}
	} else if config.TLSCAFile != "" || config.TLSCertFile != "" || config.TLSKeyFile != "" || config.TLSServerName != "" {
		return nil, errors.New("Kafka TLS files require TLS or SASL_TLS security protocol")
	}

	var mechanism sasl.Mechanism
	if useSASL {
		mechanism, err = loadSASLMechanism(config)
		if err != nil {
			return nil, err
		}
	} else if config.SASLMechanism != "" || config.Username != "" || config.Password != "" {
		return nil, errors.New("Kafka SASL credentials require a SASL security protocol")
	}

	return &Connection{
		clientID: strings.TrimSpace(config.ClientID), dialTimeout: dialTimeout,
		tlsConfig: tlsConfig, mechanism: mechanism,
	}, nil
}

// Dialer creates a kafka-go dialer for readers and administrative checks.
func (connection *Connection) Dialer() *segmentio.Dialer {
	return &segmentio.Dialer{
		ClientID: connection.clientID, Timeout: connection.dialTimeout,
		DualStack: true, TLS: cloneTLSConfig(connection.tlsConfig), SASLMechanism: connection.mechanism,
	}
}

// Transport creates an independently owned kafka-go writer transport.
func (connection *Connection) Transport() *segmentio.Transport {
	return &segmentio.Transport{
		ClientID: connection.clientID, DialTimeout: connection.dialTimeout,
		TLS: cloneTLSConfig(connection.tlsConfig), SASL: connection.mechanism,
	}
}

func loadTLSConfig(config ConnectionConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: strings.TrimSpace(config.TLSServerName)}
	if config.TLSCAFile != "" {
		payload, err := os.ReadFile(config.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read Kafka TLS CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(payload) {
			return nil, errors.New("Kafka TLS CA file contains no certificates")
		}
		tlsConfig.RootCAs = pool
	}
	if (config.TLSCertFile == "") != (config.TLSKeyFile == "") {
		return nil, errors.New("Kafka TLS client certificate and key must be configured together")
	}
	if config.TLSCertFile != "" {
		certificate, err := tls.LoadX509KeyPair(config.TLSCertFile, config.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load Kafka TLS client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	return tlsConfig, nil
}

func loadSASLMechanism(config ConnectionConfig) (sasl.Mechanism, error) {
	if strings.TrimSpace(config.Username) == "" || config.Password == "" {
		return nil, errors.New("Kafka SASL username and password are required")
	}
	switch config.SASLMechanism {
	case SASLMechanismPlain:
		return plain.Mechanism{Username: config.Username, Password: config.Password}, nil
	case SASLMechanismSCRAMSHA256:
		return scram.Mechanism(scram.SHA256, config.Username, config.Password)
	case SASLMechanismSCRAMSHA512:
		return scram.Mechanism(scram.SHA512, config.Username, config.Password)
	default:
		return nil, fmt.Errorf("unsupported Kafka SASL mechanism %q", config.SASLMechanism)
	}
}

func cloneTLSConfig(config *tls.Config) *tls.Config {
	if config == nil {
		return nil
	}
	return config.Clone()
}
