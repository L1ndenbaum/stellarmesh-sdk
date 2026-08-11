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
	"time"

	httpserver "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/server"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/application"
	serviceauth "github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/auth"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/config"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/infrastructure/filesink"
	kafkapub "github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/infrastructure/kafka"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/interfaces/console"
	httpapi "github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/interfaces/http"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/observability"
)

const kafkaStartupCheckTimeout = 10 * time.Second

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
	metrics := observability.NewMetrics()
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	runtimeCtx, cancelRuntime := context.WithCancel(context.Background())
	defer cancelRuntime()
	authenticator, err := serviceauth.LoadFile(cfg.AuthFile)
	if err != nil {
		return err
	}

	publisher, err := kafkapub.NewPublisher(cfg.KafkaBrokers, cfg.KafkaTopic, cfg.KafkaConnection)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, publisher.Close()) }()
	checkCtx, cancelCheck := context.WithTimeout(signalCtx, kafkaStartupCheckTimeout)
	if err := publisher.Check(checkCtx); err != nil {
		cancelCheck()
		return fmt.Errorf("kafka startup check failed for topic=%q: %w", cfg.KafkaTopic, err)
	}
	cancelCheck()

	fallback, err := filesink.NewKafkaFallbackStore(filesink.Config{
		RootDir: cfg.SpoolDir, MaxBytes: cfg.SpoolMaxBytes, SegmentBytes: cfg.SpoolSegmentBytes,
		ReplayBatchSize: cfg.SpoolReplayBatchSize, Observer: metrics,
	})
	if err != nil {
		return err
	}
	replayDone := fallback.StartReplay(runtimeCtx, publisher, cfg.ReplayInterval, cfg.PublishTimeout, func(err error) {
		if err != nil {
			log.Printf("logging fallback replay failed: %v", err)
			return
		}
		metrics.SetReady(true)
	})
	service := application.New(application.Config{
		FlushInterval: cfg.FlushInterval, PublishTimeout: cfg.PublishTimeout,
		QueueCapacityEvents: cfg.QueueCapacityEvents,
		MaxBatchSize:        cfg.MaxBatchSize, MaxRequestEvents: cfg.MaxRequestEvents, Observer: metrics,
	}, []application.BatchSink{&console.Sink{Writer: os.Stdout, Color: cfg.ConsoleColor}}, fallback, publisher)
	service.Start(runtimeCtx)
	metrics.SetReady(true)

	handler := httpapi.NewHandler(service, authenticator, metrics)
	server := httpserver.New(cfg.HTTPServerConfig(), httpapi.NewRouter(handler, metrics))
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	log.Printf("stellarmesh logging ingester listening on %s", cfg.Addr)
	select {
	case <-signalCtx.Done():
	case serveErr := <-serverErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			result = errors.Join(result, fmt.Errorf("serve logging API: %w", serveErr))
		}
	}

	metrics.SetReady(false)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	result = errors.Join(result, server.Shutdown(shutdownCtx))
	result = errors.Join(result, service.Shutdown(shutdownCtx))
	cancelRuntime()
	<-replayDone
	return result
}
