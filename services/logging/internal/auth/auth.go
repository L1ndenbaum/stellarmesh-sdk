// Package auth authenticates service-bound logging credentials.
package auth

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const minimumTokenLength = 32

// FileConfig is the mounted secret format used by the logging service.
type FileConfig struct {
	Services map[string][]string `json:"services"`
}

type credential struct {
	service string
	digest  [sha256.Size]byte
}

// Authenticator maps opaque token digests to one service identity.
type Authenticator struct {
	credentials []credential
}

// LoadFile reads and validates a service-bound token configuration.
func LoadFile(path string) (*Authenticator, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("logging auth file is required")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read logging auth file: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var config FileConfig
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode logging auth file: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("logging auth file must contain one JSON value")
		}
		return nil, fmt.Errorf("decode logging auth file: %w", err)
	}
	return New(config)
}

// New validates an in-memory service-bound token configuration.
func New(config FileConfig) (*Authenticator, error) {
	if len(config.Services) == 0 {
		return nil, errors.New("logging auth config requires at least one service")
	}
	seen := make(map[[sha256.Size]byte]string)
	credentials := make([]credential, 0)
	for service, tokens := range config.Services {
		if strings.TrimSpace(service) == "" || service != strings.TrimSpace(service) {
			return nil, errors.New("logging auth service names must be non-empty and trimmed")
		}
		if len(tokens) == 0 {
			return nil, fmt.Errorf("logging auth service %q requires at least one token", service)
		}
		for _, token := range tokens {
			if utf8.RuneCountInString(token) < minimumTokenLength {
				return nil, fmt.Errorf("logging auth token for service %q must contain at least %d characters", service, minimumTokenLength)
			}
			digest := sha256.Sum256([]byte(token))
			if previous, ok := seen[digest]; ok {
				return nil, fmt.Errorf("logging auth token is duplicated for services %q and %q", previous, service)
			}
			seen[digest] = service
			credentials = append(credentials, credential{service: service, digest: digest})
		}
	}
	return &Authenticator{credentials: credentials}, nil
}

// Authenticate returns the service identity bound to an opaque token.
func (authenticator *Authenticator) Authenticate(token string) (string, bool) {
	if authenticator == nil || token == "" {
		return "", false
	}
	digest := sha256.Sum256([]byte(token))
	matched := ""
	for _, candidate := range authenticator.credentials {
		if subtle.ConstantTimeCompare(digest[:], candidate.digest[:]) == 1 {
			matched = candidate.service
		}
	}
	return matched, matched != ""
}
