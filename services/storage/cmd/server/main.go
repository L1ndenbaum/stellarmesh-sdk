package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	httpserver "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/server"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage/s3store"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/storagecontract"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/application"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/config"
	httpapi "github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/interfaces/http"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/observability"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (result error) {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	accessFile, err := os.Open(cfg.AccessFile)
	if err != nil {
		return fmt.Errorf("打开 storage 访问配置: %w", err)
	}
	policy, decodeErr := storagecontract.DecodePolicy(accessFile)
	closeErr := accessFile.Close()
	if decodeErr != nil || closeErr != nil {
		return errors.Join(decodeErr, closeErr)
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	runtimeCtx, cancelRuntime := context.WithCancel(context.Background())
	defer cancelRuntime()
	metrics := observability.NewMetrics()
	awsCfg, err := awsconfig.LoadDefaultConfig(signalCtx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return fmt.Errorf("加载 AWS 配置: %w", err)
	}
	stores := make(map[string]application.Store)
	for name, namespace := range policy.Namespaces() {
		store, err := s3store.New(signalCtx, s3store.Config{
			Region: cfg.Region, Namespace: namespace, Endpoint: cfg.Endpoint,
			PresignEndpoint: cfg.PresignEndpoint, UsePathStyle: cfg.UsePathStyle,
			DefaultPresignTTL: cfg.DefaultPresignTTL, MaxPresignTTL: cfg.MaxPresignTTL,
		}, s3store.WithAWSConfig(awsCfg), s3store.WithObserver(metrics))
		if err != nil {
			return fmt.Errorf("构造 namespace store: %w", err)
		}
		stores[name] = store
	}
	registry := application.NewRegistry(stores)
	health := application.NewHealth(registry)
	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		health.Run(runtimeCtx, cfg.S3CheckTimeout, cfg.S3CheckInterval)
	}()

	handler := httpapi.NewHandler(registry, policy, health)
	server := httpserver.New(cfg.HTTPServerConfig(), httpapi.NewRouter(handler, metrics))
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	log.Printf("stellarmesh storage service listening on %s", cfg.Addr)
	select {
	case <-signalCtx.Done():
	case serveErr := <-serverErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			result = errors.Join(result, fmt.Errorf("serve storage API: %w", serveErr))
		}
	}

	health.SetReady(false)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	result = errors.Join(result, server.Shutdown(shutdownCtx))
	cancelRuntime()
	select {
	case <-healthDone:
	case <-shutdownCtx.Done():
		result = errors.Join(result, shutdownCtx.Err())
	}
	return result
}
