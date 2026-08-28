#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
temporary_root=$(mktemp -d)
trap 'rm -rf "$temporary_root"' EXIT INT TERM

cp -R "$repository_root/sdk/go" "$temporary_root/sdk-go"
(
  cd "$temporary_root/sdk-go"
  GOWORK=off go test ./...
)

mkdir -p "$temporary_root/consumer"
cd "$temporary_root/consumer"
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
