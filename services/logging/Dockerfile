FROM golang:1.22-alpine AS builder

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
COPY services/logging ./services/logging
RUN go work init ./sdk/go ./services/logging \
    && go work edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.1.0=./sdk/go
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /out/stellarmesh-logging-service ./services/logging/cmd/ingester

FROM alpine:3.20
RUN apk add --no-cache ca-certificates su-exec \
    && adduser -D -H -u 10001 appuser \
    && mkdir -p /var/lib/stellarmesh-logging/spool/regular /var/lib/stellarmesh-logging/spool/priority \
    && chmod 700 /var/lib/stellarmesh-logging /var/lib/stellarmesh-logging/spool \
        /var/lib/stellarmesh-logging/spool/regular /var/lib/stellarmesh-logging/spool/priority \
    && chown -R appuser:appuser /var/lib/stellarmesh-logging
COPY services/logging/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY --from=builder /out/stellarmesh-logging-service /usr/local/bin/stellarmesh-logging-service
EXPOSE 8091
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["stellarmesh-logging-service"]
