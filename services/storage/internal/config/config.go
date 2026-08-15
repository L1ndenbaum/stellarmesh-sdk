// Package config 加载对象存储控制面服务配置。
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/envconfig"
	httpserver "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/server"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
)

const maxRuntimeDuration = 24 * time.Hour

// Config 包含 storage-service 的运行时设置。
type Config struct {
	Addr              string
	AccessFile        string
	Region            string
	Endpoint          string
	PresignEndpoint   string
	UsePathStyle      bool
	DefaultPresignTTL time.Duration
	MaxPresignTTL     time.Duration
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	S3CheckTimeout    time.Duration
	S3CheckInterval   time.Duration
}

// Load 严格读取 STELLARMESH_STORAGE_* 和标准 AWS region 环境变量。
func Load() (Config, error) {
	loader := envconfig.NewStrictLoader()
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION"))
	}
	cfg := Config{
		Addr:              envconfig.String("STELLARMESH_STORAGE_ADDR", ":8090"),
		AccessFile:        envconfig.String("STELLARMESH_STORAGE_ACCESS_FILE", ""),
		Region:            region,
		Endpoint:          envconfig.String("STELLARMESH_STORAGE_ENDPOINT", ""),
		PresignEndpoint:   envconfig.String("STELLARMESH_STORAGE_PRESIGN_ENDPOINT", ""),
		UsePathStyle:      loader.Bool("STELLARMESH_STORAGE_USE_PATH_STYLE", false),
		DefaultPresignTTL: loader.Duration("STELLARMESH_STORAGE_DEFAULT_PRESIGN_TTL", 15*time.Minute),
		MaxPresignTTL:     loader.Duration("STELLARMESH_STORAGE_MAX_PRESIGN_TTL", time.Hour),
		ReadHeaderTimeout: loader.Duration("STELLARMESH_STORAGE_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       loader.Duration("STELLARMESH_STORAGE_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:      loader.Duration("STELLARMESH_STORAGE_WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:       loader.Duration("STELLARMESH_STORAGE_IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout:   loader.Duration("STELLARMESH_STORAGE_SHUTDOWN_TIMEOUT", 10*time.Second),
		S3CheckTimeout:    loader.Duration("STELLARMESH_STORAGE_S3_CHECK_TIMEOUT", 5*time.Second),
		S3CheckInterval:   loader.Duration("STELLARMESH_STORAGE_S3_CHECK_INTERVAL", 30*time.Second),
	}
	if err := loader.Err(); err != nil {
		return Config{}, err
	}
	if cfg.AccessFile == "" {
		return Config{}, errors.New("STELLARMESH_STORAGE_ACCESS_FILE is required")
	}
	if cfg.Region == "" {
		return Config{}, errors.New("AWS_REGION or AWS_DEFAULT_REGION is required")
	}
	if cfg.PresignEndpoint != "" && cfg.Endpoint == "" {
		return Config{}, errors.New("STELLARMESH_STORAGE_PRESIGN_ENDPOINT requires STELLARMESH_STORAGE_ENDPOINT")
	}
	if cfg.DefaultPresignTTL < objectstorage.MinPresignTTL || cfg.DefaultPresignTTL > objectstorage.MaxPresignTTL ||
		cfg.MaxPresignTTL < objectstorage.MinPresignTTL || cfg.MaxPresignTTL > objectstorage.MaxPresignTTL ||
		cfg.DefaultPresignTTL > cfg.MaxPresignTTL {
		return Config{}, errors.New("storage presign TTL settings are outside supported bounds")
	}
	for _, setting := range []struct {
		name  string
		value time.Duration
	}{
		{name: "read header timeout", value: cfg.ReadHeaderTimeout},
		{name: "read timeout", value: cfg.ReadTimeout},
		{name: "write timeout", value: cfg.WriteTimeout},
		{name: "idle timeout", value: cfg.IdleTimeout},
		{name: "shutdown timeout", value: cfg.ShutdownTimeout},
		{name: "S3 check timeout", value: cfg.S3CheckTimeout},
		{name: "S3 check interval", value: cfg.S3CheckInterval},
	} {
		if setting.value <= 0 || setting.value > maxRuntimeDuration {
			return Config{}, fmt.Errorf("storage %s is outside supported bounds", setting.name)
		}
	}
	return cfg, nil
}

// HTTPServerConfig 返回设置了边界的 HTTP 服务配置。
func (cfg Config) HTTPServerConfig() httpserver.Config {
	return httpserver.Config{
		Addr: cfg.Addr, ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
	}
}
