package main

import (
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	stellarlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	stellarkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
)

func main() {
	_ = gateway.WithRequestID(gateway.RequestIDConfig{})
	_ = stellarlogging.LevelInfo
	_ = objectstorage.ErrNotFound
	_, _ = stellarkafka.NewConnection(stellarkafka.ConnectionConfig{})
}
