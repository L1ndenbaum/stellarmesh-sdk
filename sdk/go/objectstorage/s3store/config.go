// Package s3store 实现 AWS S3 与 S3-compatible 对象存储适配器。
package s3store

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const defaultPresignTTL = 15 * time.Minute

// Config 描述 namespace 绑定和 S3 endpoint 行为。
type Config struct {
	Region            string
	Namespace         objectstorage.Namespace
	Endpoint          string
	PresignEndpoint   string
	UsePathStyle      bool
	DefaultPresignTTL time.Duration
	MaxPresignTTL     time.Duration
}

// Option 调整 AWS 凭据、Transport 或观测实现。
type Option interface {
	apply(*optionState) error
}

type option struct {
	name string
	fn   func(*optionState) error
}

func (o *option) apply(state *optionState) error {
	if _, exists := state.used[o.name]; exists {
		return fmt.Errorf("%w: option %s 重复", objectstorage.ErrInvalidArgument, o.name)
	}
	state.used[o.name] = struct{}{}
	return o.fn(state)
}

type optionState struct {
	used        map[string]struct{}
	awsConfig   *aws.Config
	credentials aws.CredentialsProvider
	httpClient  aws.HTTPClient
	observer    objectstorage.Observer
}

// WithAWSConfig 注入预先加载的 AWS 配置；Config.Region 仍具有最高优先级。
func WithAWSConfig(cfg aws.Config) Option {
	return &option{name: "aws-config", fn: func(state *optionState) error {
		state.awsConfig = &cfg
		return nil
	}}
}

// WithCredentialsProvider 覆盖 AWS 配置中的凭据提供器。
func WithCredentialsProvider(provider aws.CredentialsProvider) Option {
	return &option{name: "credentials-provider", fn: func(state *optionState) error {
		if isNil(provider) {
			return fmt.Errorf("%w: credentials provider 不能为 nil", objectstorage.ErrInvalidArgument)
		}
		state.credentials = provider
		return nil
	}}
}

// WithHTTPClient 覆盖 AWS SDK 使用的 HTTP Client。
func WithHTTPClient(client aws.HTTPClient) Option {
	return &option{name: "http-client", fn: func(state *optionState) error {
		if isNil(client) {
			return fmt.Errorf("%w: HTTP client 不能为 nil", objectstorage.ErrInvalidArgument)
		}
		state.httpClient = client
		return nil
	}}
}

// WithObserver 安装不含敏感字段的操作观察器。
func WithObserver(observer objectstorage.Observer) Option {
	return &option{name: "observer", fn: func(state *optionState) error {
		if isNil(observer) {
			return fmt.Errorf("%w: observer 不能为 nil", objectstorage.ErrInvalidArgument)
		}
		state.observer = observer
		return nil
	}}
}

// New 构造 namespace 绑定的 S3 客户端。
func New(ctx context.Context, cfg Config, options ...Option) (*Client, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	state := optionState{used: make(map[string]struct{})}
	for _, current := range options {
		if isNil(current) {
			return nil, fmt.Errorf("%w: option 不能为 nil", objectstorage.ErrInvalidArgument)
		}
		if err := current.apply(&state); err != nil {
			return nil, err
		}
	}

	awsCfg, err := loadAWSConfig(ctx, cfg.Region, state)
	if err != nil {
		return nil, &objectstorage.Error{Kind: objectstorage.ErrUnavailable, Operation: "configure", Err: err}
	}
	serviceClient := newS3Client(awsCfg, cfg.Endpoint, cfg.UsePathStyle)
	presignEndpoint := cfg.PresignEndpoint
	if presignEndpoint == "" {
		presignEndpoint = cfg.Endpoint
	}
	presignServiceClient := serviceClient
	if presignEndpoint != cfg.Endpoint {
		presignServiceClient = newS3Client(awsCfg, presignEndpoint, cfg.UsePathStyle)
	}
	return newClient(cfg, serviceClient, s3.NewPresignClient(presignServiceClient), state.observer), nil
}

func normalizeConfig(cfg Config) (Config, error) {
	if strings.TrimSpace(cfg.Region) == "" || cfg.Region != strings.TrimSpace(cfg.Region) {
		return Config{}, fmt.Errorf("%w: region 不能为空", objectstorage.ErrInvalidArgument)
	}
	namespace, err := objectstorage.NormalizeNamespace(cfg.Namespace)
	if err != nil {
		return Config{}, err
	}
	cfg.Namespace = namespace
	if cfg.PresignEndpoint != "" && cfg.Endpoint == "" {
		return Config{}, fmt.Errorf("%w: presign endpoint 不能脱离内部 endpoint 单独配置", objectstorage.ErrInvalidArgument)
	}
	for name, endpoint := range map[string]string{"endpoint": cfg.Endpoint, "presign endpoint": cfg.PresignEndpoint} {
		if endpoint == "" {
			continue
		}
		parsed, parseErr := url.Parse(endpoint)
		if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return Config{}, fmt.Errorf("%w: %s 必须是无 query/fragment 的 HTTP(S) URL", objectstorage.ErrInvalidArgument, name)
		}
	}
	if cfg.DefaultPresignTTL == 0 {
		cfg.DefaultPresignTTL = defaultPresignTTL
	}
	if cfg.MaxPresignTTL == 0 {
		cfg.MaxPresignTTL = objectstorage.MaxPresignTTL
	}
	if cfg.MaxPresignTTL < objectstorage.MinPresignTTL || cfg.MaxPresignTTL > objectstorage.MaxPresignTTL {
		return Config{}, fmt.Errorf("%w: max presign TTL 必须在 1 分钟到 1 小时之间", objectstorage.ErrInvalidArgument)
	}
	if cfg.DefaultPresignTTL < objectstorage.MinPresignTTL || cfg.DefaultPresignTTL > cfg.MaxPresignTTL {
		return Config{}, fmt.Errorf("%w: default presign TTL 必须在 1 分钟到 max presign TTL 之间", objectstorage.ErrInvalidArgument)
	}
	return cfg, nil
}

func loadAWSConfig(ctx context.Context, region string, state optionState) (aws.Config, error) {
	var cfg aws.Config
	var err error
	if state.awsConfig != nil {
		cfg = state.awsConfig.Copy()
	} else {
		loadOptions := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
		if state.credentials != nil {
			loadOptions = append(loadOptions, awsconfig.WithCredentialsProvider(state.credentials))
		}
		if state.httpClient != nil {
			loadOptions = append(loadOptions, awsconfig.WithHTTPClient(state.httpClient))
		}
		cfg, err = awsconfig.LoadDefaultConfig(ctx, loadOptions...)
		if err != nil {
			return aws.Config{}, err
		}
	}
	cfg.Region = region
	if state.credentials != nil {
		cfg.Credentials = aws.NewCredentialsCache(state.credentials)
	}
	if state.httpClient != nil {
		cfg.HTTPClient = state.httpClient
	}
	return cfg, nil
}

func newS3Client(cfg aws.Config, endpoint string, pathStyle bool) *s3.Client {
	return s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.UsePathStyle = pathStyle
		if endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
		}
	})
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
