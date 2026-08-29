package main

import (
	"context"
	"log/slog"

	stellarlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type emitter struct{}

func (emitter) Emit(context.Context, stellarlogging.Event) bool { return true }

func main() {
	handler, err := stellarlogging.NewSlogHandler(emitter{}, stellarlogging.SlogHandlerConfig{Service: "consumer"})
	if err != nil {
		panic(err)
	}
	_ = slog.New(handler)
	if _, err := stellarlogging.NewLogger(stellarlogging.LoggerConfig{Service: "consumer", Emitter: emitter{}}); err != nil {
		panic(err)
	}
	_ = stellarlogging.ClientConfig{}
	_ = stellarlogging.NewClient
	_ = stellarlogging.BatchIngestRequest{}
}
