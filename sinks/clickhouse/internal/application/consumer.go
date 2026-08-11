package application

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Source fetches one source message at a time.
type Source interface {
	FetchMessage(context.Context) (Message, error)
}

// BatchProcessor durably processes and commits source messages.
type BatchProcessor interface {
	ProcessBatch(context.Context, []Message) error
}

// ConsumerConfig controls batching, retry, and shutdown drain behavior.
type ConsumerConfig struct {
	BatchSize       int
	BatchMaxBytes   int64
	FlushInterval   time.Duration
	RetryInterval   time.Duration
	ShutdownTimeout time.Duration
	Observer        Observer
	OnError         func(error)
}

// Run consumes until cancellation and retains failed batches for retry.
func Run(ctx context.Context, source Source, processor BatchProcessor, config ConsumerConfig) error {
	if source == nil || processor == nil {
		return errors.New("Kafka source and batch processor are required")
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 500
	}
	if config.BatchMaxBytes <= 0 {
		config.BatchMaxBytes = 16 << 20
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = time.Second
	}
	if config.RetryInterval <= 0 {
		config.RetryInterval = time.Second
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 10 * time.Second
	}

	batch := make([]Message, 0, config.BatchSize)
	var batchBytes int64
	var deferred *Message
	var batchStarted time.Time
	flush := func(flushCtx context.Context) error {
		if len(batch) == 0 {
			return nil
		}
		processing := append([]Message(nil), batch...)
		if err := processor.ProcessBatch(flushCtx, processing); err != nil {
			setReady(config.Observer, false)
			reportError(config.OnError, err)
			return err
		}
		batch = batch[:0]
		batchBytes = 0
		batchStarted = time.Time{}
		setPending(config.Observer, 0)
		setPendingBytes(config.Observer, 0)
		setReady(config.Observer, true)
		return nil
	}
	drain := func() error {
		setReady(config.Observer, false)
		defer setReady(config.Observer, false)
		drainCtx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if err := flush(drainCtx); err != nil {
			return fmt.Errorf("drain ClickHouse sink batch: %w", err)
		}
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return drain()
		}
		if len(batch) >= config.BatchSize || batchBytes >= config.BatchMaxBytes {
			if err := flush(ctx); err != nil {
				if !waitForRetry(ctx, config.RetryInterval) {
					return drain()
				}
			}
			continue
		}
		if deferred != nil && len(batch) > 0 {
			if err := flush(ctx); err != nil {
				if !waitForRetry(ctx, config.RetryInterval) {
					return drain()
				}
			}
			continue
		}

		var message Message
		if deferred != nil {
			message = *deferred
			deferred = nil
		} else {
			fetchCtx := ctx
			cancel := func() {}
			if len(batch) > 0 {
				fetchCtx, cancel = context.WithDeadline(ctx, batchStarted.Add(config.FlushInterval))
			}
			fetched, err := source.FetchMessage(fetchCtx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return drain()
				}
				if errors.Is(err, context.DeadlineExceeded) && len(batch) > 0 {
					if flushErr := flush(ctx); flushErr != nil && !waitForRetry(ctx, config.RetryInterval) {
						return drain()
					}
					continue
				}
				observeOperation(config.Observer, "kafka_fetch", "failed")
				setReady(config.Observer, false)
				reportError(config.OnError, fmt.Errorf("fetch Kafka message: %w", err))
				if !waitForRetry(ctx, config.RetryInterval) {
					return drain()
				}
				continue
			}
			observeOperation(config.Observer, "kafka_fetch", "success")
			if config.Observer != nil {
				config.Observer.ObserveMessages("fetched", 1)
			}
			setReady(config.Observer, true)
			message = fetched
		}
		messageBytes := int64(len(message.Key) + len(message.Value))
		if len(batch) > 0 && batchBytes+messageBytes > config.BatchMaxBytes {
			deferred = &message
			continue
		}
		if len(batch) == 0 {
			batchStarted = time.Now()
		}
		batch = append(batch, message)
		batchBytes += messageBytes
		setPending(config.Observer, len(batch))
		setPendingBytes(config.Observer, batchBytes)
		if batchBytes >= config.BatchMaxBytes {
			if err := flush(ctx); err != nil && !waitForRetry(ctx, config.RetryInterval) {
				return drain()
			}
		}
	}
}

func waitForRetry(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func setReady(observer Observer, ready bool) {
	if observer != nil {
		observer.SetReady(ready)
	}
}

func setPending(observer Observer, count int) {
	if observer != nil {
		observer.SetPendingMessages(count)
	}
}

func setPendingBytes(observer Observer, size int64) {
	if observer != nil {
		observer.SetPendingBytes(size)
	}
}

func observeOperation(observer Observer, operation, result string) {
	if observer != nil {
		observer.ObserveOperation(operation, result)
	}
}

func reportError(callback func(error), err error) {
	if callback != nil {
		func() {
			defer func() { _ = recover() }()
			callback(err)
		}()
	}
}
