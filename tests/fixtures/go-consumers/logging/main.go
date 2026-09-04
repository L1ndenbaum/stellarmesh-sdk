package main

import (
	"context"
	"io"
	"log/slog"

	stellarlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func main() {
	base := slog.NewJSONHandler(io.Discard, nil)
	handler, err := stellarlogging.NewSanitizingHandler(base, stellarlogging.HandlerOptions{
		ContextAttrs: func(context.Context) []slog.Attr {
			return []slog.Attr{slog.String("request_id", "request-1")}
		},
	})
	if err != nil {
		panic(err)
	}
	slog.New(handler).InfoContext(context.Background(), "request completed", "token", "secret")
}
