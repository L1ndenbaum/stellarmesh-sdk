package main

import (
	"context"
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
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	metrics := observability.NewMetrics()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	authenticator, err := serviceauth.LoadFile(cfg.AuthFile)
	if err != nil {
		log.Fatal(err)
	}

	publisher, err := kafkapub.NewPublisher(cfg.KafkaBrokers, cfg.KafkaTopic, cfg.KafkaConnection)
	if err != nil {
		log.Fatal(err)
	}
	checkCtx, cancelCheck := context.WithTimeout(ctx, kafkaStartupCheckTimeout)
	if err := publisher.Check(checkCtx); err != nil {
		cancelCheck()
		log.Fatalf("kafka startup check failed for topic=%q: %v", cfg.KafkaTopic, err)
	}
	cancelCheck()
	defer func() {
		if err := publisher.Close(); err != nil {
			log.Printf("kafka publisher close failed: %v", err)
		}
	}()

	fallback, err := filesink.NewKafkaFallbackStore(filesink.Config{
		RootDir: cfg.SpoolDir, MaxBytes: cfg.SpoolMaxBytes, SegmentBytes: cfg.SpoolSegmentBytes,
		ReplayBatchSize: cfg.SpoolReplayBatchSize, Observer: metrics,
	})
	if err != nil {
		log.Fatal(err)
	}
	fallback.StartReplay(ctx, publisher, cfg.ReplayInterval, func(err error) {
		if err != nil {
			log.Printf("logging fallback replay failed: %v", err)
			return
		}
		metrics.SetReady(true)
	})
	service := application.New(application.Config{
		FlushInterval: cfg.FlushInterval, QueueCapacityEvents: cfg.QueueCapacityEvents,
		MaxBatchSize: cfg.MaxBatchSize, MaxRequestEvents: cfg.MaxRequestEvents, Observer: metrics,
	}, []application.BatchSink{&console.Sink{Writer: os.Stdout, Color: cfg.ConsoleColor}}, fallback, publisher)
	serviceCtx, stopService := context.WithCancel(context.Background())
	defer stopService()
	service.Start(serviceCtx)
	metrics.SetReady(true)

	handler := httpapi.NewHandler(service, authenticator, metrics)
	server := httpserver.New(cfg.HTTPServerConfig(), httpapi.NewRouter(handler, metrics))
	go func() {
		<-ctx.Done()
		metrics.SetReady(false)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("logging server shutdown failed: %v", err)
		}
		if err := service.Shutdown(shutdownCtx); err != nil {
			log.Printf("logging queue drain failed: %v", err)
		}
		stopService()
	}()

	log.Printf("stellarmesh logging ingester listening on %s", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
