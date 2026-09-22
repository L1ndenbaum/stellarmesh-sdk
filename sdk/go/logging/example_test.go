package logging_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func ExampleNewSanitizingHandler() {
	var output bytes.Buffer
	handler, err := logging.NewSanitizingHandler(slog.NewJSONHandler(&output, nil), logging.HandlerOptions{})
	if err != nil {
		panic(err)
	}
	logger := slog.New(handler)
	logger.Info("示例请求", "password", "example-secret")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		panic(err)
	}
	// Buffer 无关闭操作；换成文件时由应用负责关闭。
	fmt.Println(record["password"])
	// Output: [REDACTED]
}
