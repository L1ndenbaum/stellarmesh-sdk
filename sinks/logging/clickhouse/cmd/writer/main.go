package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	httpserver "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/server"
	sharedkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
	"github.com/L1ndenbaum/stellarmesh-sdk/sinks/logging/clickhouse/internal/application"
	"github.com/L1ndenbaum/stellarmesh-sdk/sinks/logging/clickhouse/internal/config"
	"github.com/L1ndenbaum/stellarmesh-sdk/sinks/logging/clickhouse/internal/infrastructure"
	"github.com/L1ndenbaum/stellarmesh-sdk/sinks/logging/clickhouse/internal/observability"
	segmentio "github.com/segmentio/kafka-go"
)

const startupCheckTimeout = 10 * time.Second
const kafkaFetchProtocolOverhead = 64 << 10

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
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	metrics := observability.NewMetrics()
	monitorServer := httpserver.New(httpserver.Config{
		Addr: cfg.ObservabilityAddr, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}, observability.NewRouter(metrics))
	listener, err := net.Listen("tcp", cfg.ObservabilityAddr)
	if err != nil {
		return fmt.Errorf("listen for ClickHouse sink observability: %w", err)
	}
	serverErrors := make(chan error, 1)
	go func() {
		if serveErr := monitorServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErrors <- serveErr
			cancel()
		}
	}()
	defer func() {
		metrics.SetReady(false)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer shutdownCancel()
		result = errors.Join(result, monitorServer.Shutdown(shutdownCtx))
	}()

	connection, err := sharedkafka.NewConnection(cfg.KafkaConnection)
	if err != nil {
		return err
	}
	if err := checkTopic(ctx, connection, cfg.KafkaBrokers, cfg.KafkaTopic); err != nil {
		return err
	}

	dlqConnection := cfg.KafkaConnection
	dlqConnection.ClientID = "stellarmesh-logging-clickhouse-dlq"
	deadLetters, err := infrastructure.NewDeadLetterPublisher(
		cfg.KafkaBrokers, cfg.KafkaDLQTopic, dlqConnection, deadLetterBatchBytes(cfg.MaxSourceMessageBytes),
	)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, deadLetters.Close()) }()
	if err := withStartupTimeout(ctx, deadLetters.Check); err != nil {
		return fmt.Errorf("check Kafka DLQ topic: %w", err)
	}

	writer, err := infrastructure.NewWriter(infrastructure.WriterConfig{
		BaseURL: cfg.ClickHouseHTTPURL, Database: cfg.ClickHouseDatabase,
		Username: cfg.ClickHouseUser, Password: cfg.ClickHousePassword, Timeout: cfg.HTTPTimeout,
	})
	if err != nil {
		return err
	}
	if err := withStartupTimeout(ctx, writer.Check); err != nil {
		return fmt.Errorf("check ClickHouse runtime access: %w", err)
	}

	reader := segmentio.NewReader(segmentio.ReaderConfig{
		Brokers: cfg.KafkaBrokers, Topic: cfg.KafkaTopic, GroupID: cfg.KafkaGroupID,
		Dialer: connection.Dialer(), MinBytes: 1,
		MaxBytes: int(cfg.MaxSourceMessageBytes + kafkaFetchProtocolOverhead), QueueCapacity: 1,
		CommitInterval: 0,
	})
	source, err := infrastructure.NewKafkaSource(reader)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, source.Close()) }()
	processor, err := application.NewProcessor(application.ProcessorConfig{
		Inserter: writer, DeadLetters: deadLetters, Committer: source, Observer: metrics,
		MaxSourceMessageBytes: cfg.MaxSourceMessageBytes,
	})
	if err != nil {
		return err
	}

	metrics.SetReady(true)
	log.Printf(
		"clickhouse sink consuming topic=%s group=%s dlq=%s observability=%s",
		cfg.KafkaTopic, cfg.KafkaGroupID, cfg.KafkaDLQTopic, cfg.ObservabilityAddr,
	)
	result = application.Run(ctx, source, processor, application.ConsumerConfig{
		BatchSize: cfg.BatchSize, BatchMaxBytes: cfg.BatchMaxBytes, FlushInterval: cfg.FlushInterval,
		RetryInterval: cfg.RetryInterval, ShutdownTimeout: cfg.ShutdownTimeout, Observer: metrics,
		OnError: func(err error) { log.Printf("clickhouse sink processing failed: %v", err) },
	})
	select {
	case serveErr := <-serverErrors:
		result = errors.Join(result, fmt.Errorf("ClickHouse sink observability server: %w", serveErr))
	default:
	}
	return result
}

func checkTopic(
	ctx context.Context,
	connection *sharedkafka.Connection,
	brokers []string,
	topic string,
) error {
	return withStartupTimeout(ctx, func(checkCtx context.Context) error {
		return sharedkafka.CheckTopic(checkCtx, connection.Dialer(), brokers, topic)
	})
}

func withStartupTimeout(ctx context.Context, check func(context.Context) error) error {
	checkCtx, cancel := context.WithTimeout(ctx, startupCheckTimeout)
	defer cancel()
	return check(checkCtx)
}

func deadLetterBatchBytes(sourceBytes int64) int64 {
	return ((sourceBytes + 2) / 3 * 4) + (16 << 10)
}
