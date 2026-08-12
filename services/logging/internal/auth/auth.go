// Package auth 验证与服务绑定的日志凭据。
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

// FileConfig 是日志服务使用的挂载 Secret 格式。
type FileConfig struct {
	Services map[string][]string `json:"services"`
}

type credential struct {
	service string
	digest  [sha256.Size]byte
}

// Authenticator 将不透明 token 摘要映射到服务身份。
type Authenticator struct {
	credentials []credential
}

// LoadFile 读取并校验与服务绑定的 token 配置。
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

// New 校验内存中的服务绑定 token 配置。
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

// Authenticate 返回与不透明 token 绑定的服务身份。
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
