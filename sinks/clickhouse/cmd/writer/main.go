package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sinks/clickhouse/internal/application"
	"github.com/L1ndenbaum/stellarmesh-sdk/sinks/clickhouse/internal/config"
	"github.com/L1ndenbaum/stellarmesh-sdk/sinks/clickhouse/internal/infrastructure"
	segmentio "github.com/segmentio/kafka-go"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	reader := segmentio.NewReader(segmentio.ReaderConfig{
		Brokers: cfg.KafkaBrokers, Topic: cfg.KafkaTopic, GroupID: cfg.KafkaGroupID,
		MinBytes: 1, MaxBytes: 10e6, CommitInterval: 0,
	})
	defer reader.Close()
	writer := infrastructure.NewWriter(infrastructure.WriterConfig{
		BaseURL: cfg.ClickHouseHTTPURL, Database: cfg.ClickHouseDatabase,
		Username: cfg.ClickHouseUser, Password: cfg.ClickHousePassword, Timeout: cfg.HTTPTimeout,
	})
	log.Printf("clickhouse sink consuming topic=%s group=%s", cfg.KafkaTopic, cfg.KafkaGroupID)
	if err := run(ctx, reader, writer, &kafkaCommitter{reader: reader}, cfg.BatchSize, cfg.FlushInterval); err != nil {
		log.Fatal(err)
	}
}

func run(
	ctx context.Context,
	reader *segmentio.Reader,
	inserter application.Inserter,
	committer application.Committer,
	batchSize int,
	flushInterval time.Duration,
) error {
	if batchSize <= 0 {
		batchSize = 500
	}
	if flushInterval <= 0 {
		flushInterval = time.Second
	}
	batch := make([]application.Message, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		processing := append([]application.Message(nil), batch...)
		if err := application.ProcessBatch(ctx, processing, inserter, committer); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for {
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				log.Printf("clickhouse batch flush failed: %v", err)
				time.Sleep(time.Second)
			}
			continue
		}
		fetchCtx := ctx
		cancel := func() {}
		if len(batch) > 0 {
			fetchCtx, cancel = context.WithTimeout(ctx, flushInterval)
		}
		message, err := reader.FetchMessage(fetchCtx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && len(batch) > 0 {
				if flushErr := flush(); flushErr != nil {
					log.Printf("clickhouse batch flush failed: %v", flushErr)
				}
				continue
			}
			if ctx.Err() != nil {
				return flush()
			}
			log.Printf("kafka fetch failed: %v", err)
			time.Sleep(time.Second)
			continue
		}
		batch = append(batch, application.Message{Value: message.Value, Handle: message})
	}
}

type kafkaCommitter struct {
	reader *segmentio.Reader
}

func (committer *kafkaCommitter) Commit(ctx context.Context, messages []application.Message) error {
	kafkaMessages := make([]segmentio.Message, 0, len(messages))
	for _, message := range messages {
		if kafkaMessage, ok := message.Handle.(segmentio.Message); ok {
			kafkaMessages = append(kafkaMessages, kafkaMessage)
		}
	}
	if len(kafkaMessages) == 0 {
		return nil
	}
	return committer.reader.CommitMessages(ctx, kafkaMessages...)
}
