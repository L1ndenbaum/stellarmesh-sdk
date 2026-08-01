// Package application implements ingestion, batching, and sink fan-out.
package application

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

var (
	ErrEmptyBatch    = errors.New("at least one event is required")
	ErrTooManyEvents = errors.New("request contains too many events")
	ErrQueueFull     = errors.New("logging queue is full")
	ErrShuttingDown  = errors.New("logging service is shutting down")
)

// BatchSink writes a flushed batch to a local sink.
type BatchSink interface {
	WriteBatch(context.Context, []sharedlogging.Event) error
}

// Publisher forwards accepted events to the event bus.
type Publisher interface {
	Publish(context.Context, []sharedlogging.Event) error
}

// Config controls queue and batch behavior.
type Config struct {
	FlushInterval    time.Duration
	QueueSize        int
	MaxBatchSize     int
	MaxRequestEvents int
}

// Service validates, queues, and flushes log events.
type Service struct {
	queue     chan []sharedlogging.Event
	sinks     []BatchSink
	fallback  BatchSink
	publisher Publisher
	config    Config
	mu        sync.RWMutex
	closed    bool
	startOnce sync.Once
	done      chan struct{}
}

// New creates an ingestion service.
func New(config Config, sinks []BatchSink, fallback BatchSink, publisher Publisher) *Service {
	if config.FlushInterval <= 0 {
		config.FlushInterval = 500 * time.Millisecond
	}
	if config.QueueSize <= 0 {
		config.QueueSize = 1024
	}
	if config.MaxBatchSize <= 0 {
		config.MaxBatchSize = 256
	}
	if config.MaxRequestEvents <= 0 {
		config.MaxRequestEvents = 512
	}
	return &Service{
		queue: make(chan []sharedlogging.Event, config.QueueSize), sinks: sinks, fallback: fallback,
		publisher: publisher, config: config, done: make(chan struct{}),
	}
}

// Start launches the queue worker once.
func (service *Service) Start(ctx context.Context) {
	service.startOnce.Do(func() { go service.run(ctx) })
}

// Ingest validates and enqueues events without remote I/O.
func (service *Service) Ingest(ctx context.Context, events []sharedlogging.Event) error {
	if len(events) == 0 {
		return ErrEmptyBatch
	}
	if len(events) > service.config.MaxRequestEvents {
		return ErrTooManyEvents
	}
	copied := make([]sharedlogging.Event, 0, len(events))
	for _, event := range events {
		if err := event.Validate(); err != nil {
			return err
		}
		event.Timestamp = event.Timestamp.UTC()
		event.Metadata = sharedlogging.SanitizeMetadata(event.Metadata)
		copied = append(copied, event)
	}

	service.mu.RLock()
	defer service.mu.RUnlock()
	if service.closed {
		return ErrShuttingDown
	}
	select {
	case service.queue <- copied:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return ErrQueueFull
	}
}

// Shutdown stops intake and drains queued batches.
func (service *Service) Shutdown(ctx context.Context) error {
	service.closeQueue()
	select {
	case <-service.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (service *Service) closeQueue() {
	service.mu.Lock()
	defer service.mu.Unlock()
	if !service.closed {
		service.closed = true
		close(service.queue)
	}
}

func (service *Service) run(ctx context.Context) {
	defer close(service.done)
	ticker := time.NewTicker(service.config.FlushInterval)
	defer ticker.Stop()
	pending := make([]sharedlogging.Event, 0, service.config.MaxBatchSize)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := append([]sharedlogging.Event(nil), pending...)
		pending = pending[:0]
		service.writeBatch(ctx, batch)
	}
	for {
		select {
		case events, ok := <-service.queue:
			if !ok {
				flush()
				return
			}
			pending = append(pending, events...)
			if len(pending) >= service.config.MaxBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			service.closeQueue()
			for events := range service.queue {
				pending = append(pending, events...)
			}
			flush()
			return
		}
	}
}

func (service *Service) writeBatch(ctx context.Context, events []sharedlogging.Event) {
	for _, sink := range service.sinks {
		if err := sink.WriteBatch(ctx, events); err != nil {
			log.Printf("logging sink write failed: %v", err)
		}
	}
	if service.publisher == nil {
		return
	}
	if err := service.publisher.Publish(ctx, events); err != nil {
		log.Printf("kafka publish failed: %v", err)
		if service.fallback != nil {
			if fallbackErr := service.fallback.WriteBatch(ctx, events); fallbackErr != nil {
				log.Printf("logging fallback write failed: %v", fallbackErr)
			}
		}
	}
}
