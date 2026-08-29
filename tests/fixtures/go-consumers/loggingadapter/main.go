package main

import (
	"context"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/loggingadapter"
	stellarlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type emitter struct{}

func (emitter) Emit(context.Context, stellarlogging.Event) bool { return true }

func main() {
	logger, err := loggingadapter.NewStellarmesh(loggingadapter.StellarmeshConfig{
		Service:         "consumer-gateway",
		Emitter:         emitter{},
		IncludeIdentity: true,
		TraceIDProvider: func(context.Context, gateway.AccessLog) string { return "trace-id" },
	})
	if err != nil {
		panic(err)
	}
	_ = gateway.WithAccessLogger(logger)
}
