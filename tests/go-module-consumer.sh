#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
temporary_root=$(mktemp -d)
trap 'rm -rf "$temporary_root"' EXIT INT TERM

cp -R "$repository_root/sdk/go" "$temporary_root/sdk-go"
mv "$temporary_root/sdk-go/mq/kafka" "$temporary_root/kafka-sdk"
(
  cd "$temporary_root/sdk-go"
  GOWORK=off go test ./...
	if GOWORK=off go list -m all | grep -q '^github.com/segmentio/kafka-go '; then
		echo "父 SDK 不应包含 kafka-go" >&2
		exit 1
	fi
)
(
	cd "$temporary_root/kafka-sdk"
	GOWORK=off go test ./...
)

mkdir -p "$temporary_root/parent-consumer"
cd "$temporary_root/parent-consumer"
go mod init example.com/stellarmesh-consumer >/dev/null
go mod edit -go=1.24
go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.0.0
go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go=../sdk-go

cat > main.go <<'EOF'
package main

import (
	"context"
	"log/slog"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage/s3store"
)

type emitter struct{}

func (emitter) Emit(context.Context, sharedlogging.Event) bool { return true }

func main() {
	handler, err := sharedlogging.NewSlogHandler(emitter{}, sharedlogging.SlogHandlerConfig{Service: "consumer"})
	if err != nil {
		panic(err)
	}
	_ = slog.New(handler)
	_ = gateway.WithAccessLogEmitter("consumer", emitter{})
	var _ objectstorage.Reader = (*s3store.Client)(nil)
}
EOF

GOWORK=off go mod tidy
GOWORK=off go build ./...

mkdir -p "$temporary_root/kafka-consumer"
cd "$temporary_root/kafka-consumer"
go mod init example.com/stellarmesh-kafka-consumer >/dev/null
go mod edit -go=1.24
go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.0.0
go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka=../kafka-sdk

cat > main.go <<'EOF'
package main

import (
	"context"

	stellarkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
)

func main() {
	for _, mechanism := range []stellarkafka.SASLMechanism{
		stellarkafka.SASLMechanismPlain,
		stellarkafka.SASLMechanismSCRAMSHA256,
		stellarkafka.SASLMechanismSCRAMSHA512,
	} {
		connection, err := stellarkafka.NewConnection(stellarkafka.ConnectionConfig{
			SecurityProtocol: stellarkafka.SecurityProtocolSASLPlaintext,
			SASLMechanism: mechanism,
			Username: "consumer",
			Password: "secret",
		})
		if err != nil {
			panic(err)
		}
		_ = connection.Dialer()
		_ = connection.Transport()
	}
	publisher, err := stellarkafka.NewPublisher(stellarkafka.Config{})
	if err != nil {
		panic(err)
	}
	_ = publisher.Publish(context.Background(), nil)
	_ = publisher.Check
	_ = publisher.Close()
	_ = stellarkafka.CheckTopic
	_ = stellarkafka.IsMessageTooLarge
}
EOF

GOWORK=off go mod tidy
GOWORK=off go build ./...
kafka_module_graph=$(GOWORK=off go list -m all)
if printf '%s\n' "$kafka_module_graph" | grep -Eq 'aws-sdk-go-v2|go-chi/chi|golang-jwt|redis/go-redis'; then
	echo "Kafka-only 消费者引入了非 Kafka 依赖" >&2
	exit 1
fi

mkdir -p "$temporary_root/combined-consumer"
cd "$temporary_root/combined-consumer"
go mod init example.com/stellarmesh-combined-consumer >/dev/null
go mod edit -go=1.24
go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.0.0
go mod edit -require=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.0.0
go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go=../sdk-go
go mod edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka=../kafka-sdk

cat > main.go <<'EOF'
package main

import (
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	stellarkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
)

func main() {
	_ = gateway.WithRequestID(gateway.RequestIDConfig{})
	_ = sharedlogging.LevelInfo
	_ = objectstorage.ErrNotFound
	_, _ = stellarkafka.NewConnection(stellarkafka.ConnectionConfig{})
}
EOF

GOWORK=off go mod tidy
GOWORK=off go build ./...
