package main

import (
	"context"
	"io"
	"log/slog"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	stellarlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	stellarkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
)

func main() {
	_ = gateway.WithRequestID(gateway.RequestIDConfig{})
	handler, _ := stellarlogging.NewSanitizingHandler(slog.NewJSONHandler(io.Discard, nil), stellarlogging.HandlerOptions{
		ContextAttrs: func(context.Context) []slog.Attr { return nil },
	})
	_ = slog.New(handler)
	_ = objectstorage.ErrNotFound
	_, _ = stellarkafka.NewConnection(stellarkafka.ConnectionConfig{})
}
