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

type saturationReporter interface {
	Saturated() bool
}

// Observer receives bounded ingestion metrics and readiness transitions.
type Observer interface {
	ObserveIngest(result, reason string, count int)
	SetQueueDepth(depth int)
	ObserveKafkaPublish(result string, count int)
	SetReady(ready bool)
}

// Config controls queue and batch behavior.
type Config struct {
	FlushInterval       time.Duration
	QueueCapacityEvents int
	MaxBatchSize        int
	MaxRequestEvents    int
	Observer            Observer
}

// Service validates, queues, and flushes log events.
type Service struct {
	queue        chan []sharedlogging.Event
	sinks        []BatchSink
	fallback     BatchSink
	publisher    Publisher
	config       Config
	mu           sync.Mutex
	queuedEvents int
	closed       bool
	startOnce    sync.Once
	done         chan struct{}
}

// New creates an ingestion service.
func New(config Config, sinks []BatchSink, fallback BatchSink, publisher Publisher) *Service {
	if config.FlushInterval <= 0 {
		config.FlushInterval = 500 * time.Millisecond
	}
	if config.QueueCapacityEvents <= 0 {
		config.QueueCapacityEvents = 1024
	}
	if config.MaxBatchSize <= 0 {
		config.MaxBatchSize = 256
	}
	if config.MaxRequestEvents <= 0 {
		config.MaxRequestEvents = 512
	}
	return &Service{
		queue: make(chan []sharedlogging.Event, config.QueueCapacityEvents), sinks: sinks, fallback: fallback,
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
		service.observeIngest("rejected", "empty", 1)
		return ErrEmptyBatch
	}
	if len(events) > service.config.MaxRequestEvents {
		service.observeIngest("rejected", "too_many", len(events))
		return ErrTooManyEvents
	}
	copied := make([]sharedlogging.Event, 0, len(events))
	for _, event := range events {
		if err := event.Validate(); err != nil {
			service.observeIngest("rejected", "invalid", len(events))
			return err
		}
		event.Timestamp = event.Timestamp.UTC()
		event.Metadata = sharedlogging.SanitizeMetadata(event.Metadata)
		copied = append(copied, event)
	}

	if err := ctx.Err(); err != nil {
		service.observeIngest("rejected", "context", len(events))
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		service.observeIngest("rejected", "shutting_down", len(events))
		return ErrShuttingDown
	}
	if service.queuedEvents+len(copied) > service.config.QueueCapacityEvents {
		service.observeIngest("rejected", "queue_full", len(events))
		return ErrQueueFull
	}
	select {
	case service.queue <- copied:
		service.queuedEvents += len(copied)
		service.setQueueDepth(service.queuedEvents)
		service.observeIngest("accepted", "", len(copied))
		return nil
	case <-ctx.Done():
		service.observeIngest("rejected", "context", len(events))
		return ctx.Err()
	default:
		service.observeIngest("rejected", "queue_full", len(events))
		return ErrQueueFull
	}
}

// Shutdown stops intake and drains queued batches.
func (service *Service) Shutdown(ctx context.Context) error {
	defer service.setReady(false)
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
		service.markFlushing(len(batch))
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
		service.observeKafkaPublish("failed", len(events))
		log.Printf("kafka publish failed: %v", err)
		if service.fallback != nil {
			if fallbackErr := service.fallback.WriteBatch(ctx, events); fallbackErr != nil {
				service.setReady(false)
				log.Printf("logging fallback write failed: %v", fallbackErr)
			} else if reporter, ok := service.fallback.(saturationReporter); ok {
				service.setReady(!reporter.Saturated())
			} else {
				service.setReady(true)
			}
		} else {
			service.setReady(false)
		}
		return
	}
	service.observeKafkaPublish("success", len(events))
	service.setReady(true)
}

func (service *Service) markFlushing(count int) {
	service.mu.Lock()
	service.queuedEvents -= count
	depth := service.queuedEvents
	service.mu.Unlock()
	service.setQueueDepth(depth)
}

func (service *Service) observeIngest(result, reason string, count int) {
	if service.config.Observer != nil {
		service.config.Observer.ObserveIngest(result, reason, count)
	}
}

func (service *Service) setQueueDepth(depth int) {
	if service.config.Observer != nil {
		service.config.Observer.SetQueueDepth(depth)
	}
}

func (service *Service) observeKafkaPublish(result string, count int) {
	if service.config.Observer != nil {
		service.config.Observer.ObserveKafkaPublish(result, count)
	}
}

func (service *Service) setReady(ready bool) {
	if service.config.Observer != nil {
		service.config.Observer.SetReady(ready)
	}
}
