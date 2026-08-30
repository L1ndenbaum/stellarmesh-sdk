package main

import (
	"context"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/loggingadapter"
	stellarlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	stellarkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
)

type emitter struct{}

func (emitter) Emit(context.Context, stellarlogging.Event) bool { return true }

func main() {
	_ = gateway.WithRequestID(gateway.RequestIDConfig{})
	logger, _ := loggingadapter.NewStellarmesh(loggingadapter.StellarmeshConfig{
		Service: "combined-gateway", Emitter: emitter{},
	})
	_ = gateway.WithAccessLogger(logger)
	_ = stellarlogging.Event{Kind: stellarlogging.EventKindAudit, Level: stellarlogging.LevelInfo}
	_ = objectstorage.ErrNotFound
	_, _ = stellarkafka.NewConnection(stellarkafka.ConnectionConfig{})
}
