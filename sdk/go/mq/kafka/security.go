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

// SecurityProtocol 选择 Kafka 传输加密和认证方式。
type SecurityProtocol string

const (
	SecurityProtocolPlaintext     SecurityProtocol = "PLAINTEXT"
	SecurityProtocolTLS           SecurityProtocol = "TLS"
	SecurityProtocolSASLPlaintext SecurityProtocol = "SASL_PLAINTEXT"
	SecurityProtocolSASLTLS       SecurityProtocol = "SASL_TLS"
)

// SASLMechanism 选择支持的 Kafka SASL 交互机制。
type SASLMechanism string

const (
	SASLMechanismPlain       SASLMechanism = "PLAIN"
	SASLMechanismSCRAMSHA256 SASLMechanism = "SCRAM-SHA-256"
	SASLMechanismSCRAMSHA512 SASLMechanism = "SCRAM-SHA-512"
)

// ConnectionConfig 包含可复用的 Kafka 客户端安全设置。
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

// Connection 持有 Kafka 客户端不可变的 TLS 和 SASL 设置。
type Connection struct {
	clientID    string
	dialTimeout time.Duration
	tlsConfig   *tls.Config
	mechanism   sasl.Mechanism
}

// NewConnection 校验安全设置并加载所有引用的证书。
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

// Dialer 为 reader 和管理检查创建 kafka-go dialer。
func (connection *Connection) Dialer() *segmentio.Dialer {
	return &segmentio.Dialer{
		ClientID: connection.clientID, Timeout: connection.dialTimeout,
		DualStack: true, TLS: cloneTLSConfig(connection.tlsConfig), SASLMechanism: connection.mechanism,
	}
}

// Transport 创建独立持有的 kafka-go writer 传输层。
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
