FROM golang:1.24-alpine AS builder

WORKDIR /src

ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG NO_PROXY
ARG http_proxy
ARG https_proxy
ARG no_proxy
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB}

COPY sdk/go ./sdk/go
COPY sinks/logging/clickhouse ./sinks/logging/clickhouse
RUN go work init ./sdk/go ./sdk/go/logging ./sdk/go/mq/kafka ./sinks/logging/clickhouse \
    && go work edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.5.0=./sdk/go \
    && go work edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.1.0=./sdk/go/logging \
    && go work edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0=./sdk/go/mq/kafka
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /out/stellarmesh-logging-clickhouse-sink ./sinks/logging/clickhouse/cmd/writer

FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 10001 appuser
COPY --from=builder /out/stellarmesh-logging-clickhouse-sink /usr/local/bin/stellarmesh-logging-clickhouse-sink
USER appuser
EXPOSE 8092
ENTRYPOINT ["stellarmesh-logging-clickhouse-sink"]
