package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"

	stellarlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type field struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

type testCase struct {
	Name    string `json:"name"`
	Options struct {
		MaxAttributes      int      `json:"max_attributes"`
		MaxDepth           int      `json:"max_depth"`
		MaxStringBytes     int      `json:"max_string_bytes"`
		ExtraSensitiveKeys []string `json:"extra_sensitive_keys"`
	} `json:"options"`
	StaticFields []field        `json:"static_fields"`
	Attrs        []field        `json:"attrs"`
	Want         map[string]any `json:"want"`
}

func main() {
	content, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	var cases []testCase
	if err := json.Unmarshal(content, &cases); err != nil {
		panic(err)
	}
	for _, item := range cases {
		var output bytes.Buffer
		handler, err := stellarlogging.NewSanitizingHandler(slog.NewJSONHandler(&output, nil), stellarlogging.HandlerOptions{
			MaxAttributes:      item.Options.MaxAttributes,
			MaxDepth:           item.Options.MaxDepth,
			MaxStringBytes:     item.Options.MaxStringBytes,
			ExtraSensitiveKeys: item.Options.ExtraSensitiveKeys,
		})
		if err != nil {
			panic(err)
		}
		var static []slog.Attr
		for _, attr := range item.StaticFields {
			static = append(static, slog.Any(attr.Key, attr.Value))
		}
		logger := slog.New(handler.WithAttrs(static))
		var attrs []any
		for _, attr := range item.Attrs {
			attrs = append(attrs, slog.Any(attr.Key, attr.Value))
		}
		logger.Info("contract", attrs...)
		var record map[string]any
		if err := json.Unmarshal(output.Bytes(), &record); err != nil {
			panic(err)
		}
		delete(record, "time")
		delete(record, "level")
		delete(record, "msg")
		if !reflect.DeepEqual(record, item.Want) {
			panic(fmt.Sprintf("%s: got %#v, want %#v", item.Name, record, item.Want))
		}
	}
}
