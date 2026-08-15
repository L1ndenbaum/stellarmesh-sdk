package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	clearStorageEnvironment(t)
	t.Setenv("STELLARMESH_STORAGE_ACCESS_FILE", "/run/secrets/storage-access.json")
	t.Setenv("AWS_REGION", "ap-southeast-1")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr != ":8090" || cfg.DefaultPresignTTL != 15*time.Minute || cfg.MaxPresignTTL != time.Hour ||
		cfg.S3CheckTimeout != 5*time.Second || cfg.S3CheckInterval != 30*time.Second {
		t.Fatalf("Load() = %+v", cfg)
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "缺少访问文件", key: "STELLARMESH_STORAGE_ACCESS_FILE", value: ""},
		{name: "非法布尔值", key: "STELLARMESH_STORAGE_USE_PATH_STYLE", value: "sometimes"},
		{name: "过短默认 TTL", key: "STELLARMESH_STORAGE_DEFAULT_PRESIGN_TTL", value: "10s"},
		{name: "孤立预签名端点", key: "STELLARMESH_STORAGE_PRESIGN_ENDPOINT", value: "http://public.example"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearStorageEnvironment(t)
			t.Setenv("STELLARMESH_STORAGE_ACCESS_FILE", "/run/secrets/storage-access.json")
			t.Setenv("AWS_REGION", "test-1")
			t.Setenv(test.key, test.value)
			if _, err := config.Load(); err == nil {
				t.Fatal("Load() 应拒绝非法配置")
			}
		})
	}
}

func clearStorageEnvironment(t *testing.T) {
	t.Helper()
	for _, item := range os.Environ() {
		key := item
		for index, character := range key {
			if character == '=' {
				key = key[:index]
				break
			}
		}
		if len(key) >= len("STELLARMESH_STORAGE_") && key[:len("STELLARMESH_STORAGE_")] == "STELLARMESH_STORAGE_" {
			t.Setenv(key, "")
		}
	}
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
}
