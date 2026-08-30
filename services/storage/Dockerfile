FROM golang:1.24-alpine@sha256:8bee1901f1e530bfb4a7850aa7a479d17ae3a18beb6e09064ed54cfd245b7191 AS builder

ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG NO_PROXY
ARG http_proxy
ARG https_proxy
ARG no_proxy
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB}

WORKDIR /src
COPY sdk/go ./sdk/go
COPY services/storage ./services/storage
RUN go work init ./sdk/go ./sdk/go/objectstorage ./services/storage \
    && go work edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.5.0=./sdk/go \
    && go work edit -replace=github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage@v0.1.0=./sdk/go/objectstorage
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /out/stellarmesh-storage-service ./services/storage/cmd/server

FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 10001 appuser
COPY --from=builder /out/stellarmesh-storage-service /usr/local/bin/stellarmesh-storage-service
USER appuser
EXPOSE 8090
ENTRYPOINT ["stellarmesh-storage-service"]
