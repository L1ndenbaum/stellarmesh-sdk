package gateway

// RequestIDConfig 控制传入请求 ID 的信任范围和生成方式。
type RequestIDConfig struct {
	Header    string
	MaxLength int
	Generate  func() (string, error)
}

// WithRequestID 配置请求 ID；空字段使用 SDK 安全默认值。
func WithRequestID(requestID RequestIDConfig) Option {
	return componentOption("request_id", func(config *config) error {
		config.requestID = requestID
		return nil
	})
}
