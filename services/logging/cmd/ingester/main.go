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
)

const kafkaStartupCheckTimeout = 10 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
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

	fallback := filesink.NewKafkaFallbackStore(cfg.SpoolFile, cfg.ErrorAuditFile)
	fallback.StartReplay(ctx, publisher, cfg.ReplayInterval)
	service := application.New(application.Config{
		FlushInterval: cfg.FlushInterval, QueueSize: cfg.QueueSize,
		MaxBatchSize: cfg.MaxBatchSize, MaxRequestEvents: cfg.MaxRequestEvents,
	}, []application.BatchSink{&console.Sink{Writer: os.Stdout, Color: cfg.ConsoleColor}}, fallback, publisher)
	serviceCtx, stopService := context.WithCancel(context.Background())
	defer stopService()
	service.Start(serviceCtx)

	server := httpserver.New(cfg.HTTPServerConfig(), httpapi.NewRouter(httpapi.NewHandler(service, authenticator)))
	go func() {
		<-ctx.Done()
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
