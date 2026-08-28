// Package application 实现日志接收、批处理和 sink 扇出。
package application

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

var (
	ErrEmptyBatch            = errors.New("at least one event is required")
	ErrTooManyEvents         = errors.New("request contains too many events")
	ErrEventTooLarge         = errors.New("logging event exceeds the contract size limit")
	ErrQueueFull             = errors.New("logging queue is full")
	ErrShuttingDown          = errors.New("logging service is shutting down")
	ErrDurabilityUnavailable = errors.New("logging durability is unavailable")
)

// BatchSink 将已刷新的批次写入本地 sink。
type BatchSink interface {
	WriteBatch(context.Context, []sharedlogging.Event) error
}

// Publisher 将已接受事件转发到事件总线。
type Publisher interface {
	Publish(context.Context, []sharedlogging.Event) error
}

type saturationReporter interface {
	Saturated() bool
}

// Observer 接收有界接收指标和就绪状态变化。
type Observer interface {
	ObserveIngest(result, reason string, count int)
	SetQueueDepth(depth int)
	SetQueueBytes(size int64)
	ObserveKafkaPublish(result string, count int)
	SetReady(ready bool)
}

// Config 控制队列和批处理行为。
type Config struct {
	FlushInterval       time.Duration
	PublishTimeout      time.Duration
	QueueCapacityEvents int
	QueueCapacityBytes  int64
	MaxBatchSize        int
	MaxBatchBytes       int64
	MaxRequestEvents    int
	Observer            Observer
}

// Service 校验、排队并刷新日志事件。
type Service struct {
	queue        chan queuedBatch
	sinks        []BatchSink
	fallback     BatchSink
	publisher    Publisher
	config       Config
	mu           sync.Mutex
	queuedEvents int
	queuedBytes  int64
	closed       bool
	startOnce    sync.Once
	done         chan struct{}
	cancelWorker context.CancelFunc
}

type queuedBatch struct {
	events []sharedlogging.Event
	bytes  int64
	result chan error
}

// New 创建日志接收服务。
func New(config Config, sinks []BatchSink, fallback BatchSink, publisher Publisher) *Service {
	if config.FlushInterval <= 0 {
		config.FlushInterval = 500 * time.Millisecond
	}
	if config.QueueCapacityEvents <= 0 {
		config.QueueCapacityEvents = 1024
	}
	if config.QueueCapacityBytes <= 0 {
		config.QueueCapacityBytes = 16 << 20
	}
	if config.MaxBatchSize <= 0 {
		config.MaxBatchSize = 256
	}
	if config.MaxBatchBytes <= 0 {
		config.MaxBatchBytes = 4 << 20
	}
	if config.MaxRequestEvents <= 0 {
		config.MaxRequestEvents = 512
	}
	if config.PublishTimeout <= 0 {
		config.PublishTimeout = 5 * time.Second
	}
	return &Service{
		queue: make(chan queuedBatch, config.QueueCapacityEvents), sinks: sinks, fallback: fallback,
		publisher: publisher, config: config, done: make(chan struct{}),
	}
}

// Start 启动一次队列工作线程。
func (service *Service) Start(ctx context.Context) {
	service.startOnce.Do(func() {
		workerCtx, cancel := context.WithCancel(ctx)
		service.mu.Lock()
		service.cancelWorker = cancel
		service.mu.Unlock()
		go service.run(workerCtx)
	})
}

// Ingest 校验并排队事件，然后等待 Kafka 或 fallback spool 持久确认。
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
	var requestBytes int64
	for _, event := range events {
		if err := event.Validate(); err != nil {
			service.observeIngest("rejected", "invalid", len(events))
			return err
		}
		event.Timestamp = event.Timestamp.UTC()
		event.Metadata = sharedlogging.SanitizeMetadata(event.Metadata)
		payload, err := json.Marshal(event)
		if err != nil {
			service.observeIngest("rejected", "invalid", len(events))
			return err
		}
		if len(payload) > sharedlogging.MaxEventJSONBytesV1 ||
			!sharedlogging.FitsKafkaKeyValueBudgetV1(event, len(payload)) {
			service.observeIngest("rejected", "event_too_large", len(events))
			return ErrEventTooLarge
		}
		payloadBytes := int64(len(payload))
		if payloadBytes > service.config.QueueCapacityBytes-requestBytes {
			service.observeIngest("rejected", "queue_full", len(events))
			return ErrQueueFull
		}
		requestBytes += payloadBytes
		copied = append(copied, event)
	}

	if err := ctx.Err(); err != nil {
		service.observeIngest("rejected", "context", len(events))
		return err
	}
	request := queuedBatch{events: copied, bytes: requestBytes, result: make(chan error, 1)}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		service.observeIngest("rejected", "shutting_down", len(events))
		return ErrShuttingDown
	}
	if len(copied) > service.config.QueueCapacityEvents-service.queuedEvents ||
		requestBytes > service.config.QueueCapacityBytes-service.queuedBytes {
		service.mu.Unlock()
		service.observeIngest("rejected", "queue_full", len(events))
		return ErrQueueFull
	}
	select {
	case service.queue <- request:
		service.queuedEvents += len(copied)
		service.queuedBytes += requestBytes
		service.setQueueDepth(service.queuedEvents)
		service.setQueueBytes(service.queuedBytes)
		service.mu.Unlock()
		service.observeIngest("accepted", "", len(copied))
	case <-ctx.Done():
		service.mu.Unlock()
		service.observeIngest("rejected", "context", len(events))
		return ctx.Err()
	default:
		service.mu.Unlock()
		service.observeIngest("rejected", "queue_full", len(events))
		return ErrQueueFull
	}
	select {
	case err := <-request.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown 停止接收并排空队列中的批次。
func (service *Service) Shutdown(ctx context.Context) error {
	defer service.setReady(false)
	service.Start(context.Background())
	service.closeQueue()
	select {
	case <-service.done:
		return nil
	case <-ctx.Done():
		service.cancel()
		return ctx.Err()
	}
}

func (service *Service) cancel() {
	service.mu.Lock()
	cancel := service.cancelWorker
	service.mu.Unlock()
	if cancel != nil {
		cancel()
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
	pending := make([]queuedBatch, 0, service.config.MaxBatchSize)
	pendingEvents := make([]sharedlogging.Event, 0, service.config.MaxBatchSize)
	var pendingBytes int64
	flush := func() {
		if len(pendingEvents) == 0 {
			return
		}
		batch := append([]sharedlogging.Event(nil), pendingEvents...)
		requests := append([]queuedBatch(nil), pending...)
		pending = pending[:0]
		pendingEvents = pendingEvents[:0]
		flushedBytes := pendingBytes
		pendingBytes = 0
		err := service.writeBatch(ctx, batch)
		service.markFlushed(len(batch), flushedBytes)
		for _, request := range requests {
			request.result <- err
		}
	}
	for {
		select {
		case request, ok := <-service.queue:
			if !ok {
				flush()
				return
			}
			if len(pendingEvents) > 0 &&
				(len(pendingEvents)+len(request.events) > service.config.MaxBatchSize ||
					pendingBytes+request.bytes > service.config.MaxBatchBytes) {
				flush()
			}
			pending = append(pending, request)
			pendingEvents = append(pendingEvents, request.events...)
			pendingBytes += request.bytes
			if len(pendingEvents) >= service.config.MaxBatchSize || pendingBytes >= service.config.MaxBatchBytes {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			service.closeQueue()
			for request := range service.queue {
				if len(pendingEvents) > 0 &&
					(len(pendingEvents)+len(request.events) > service.config.MaxBatchSize ||
						pendingBytes+request.bytes > service.config.MaxBatchBytes) {
					flush()
				}
				pending = append(pending, request)
				pendingEvents = append(pendingEvents, request.events...)
				pendingBytes += request.bytes
			}
			flush()
			return
		}
	}
}

func (service *Service) writeBatch(ctx context.Context, events []sharedlogging.Event) error {
	var publishErr error
	if service.publisher == nil {
		publishErr = errors.New("Kafka publisher is unavailable")
	} else {
		publishCtx, cancel := context.WithTimeout(ctx, service.config.PublishTimeout)
		publishErr = service.publisher.Publish(publishCtx, events)
		cancel()
	}
	if publishErr != nil {
		service.observeKafkaPublish("failed", len(events))
		log.Printf("kafka publish failed: %v", publishErr)
		if service.fallback != nil {
			if fallbackErr := service.fallback.WriteBatch(ctx, events); fallbackErr != nil {
				service.setReady(false)
				log.Printf("logging fallback write failed: %v", fallbackErr)
				return ErrDurabilityUnavailable
			} else if reporter, ok := service.fallback.(saturationReporter); ok {
				service.setReady(!reporter.Saturated())
			} else {
				service.setReady(true)
			}
		} else {
			service.setReady(false)
			return ErrDurabilityUnavailable
		}
		service.writeAcceptedSinks(ctx, events)
		return nil
	}
	service.observeKafkaPublish("success", len(events))
	service.setReady(true)
	service.writeAcceptedSinks(ctx, events)
	return nil
}

func (service *Service) writeAcceptedSinks(ctx context.Context, events []sharedlogging.Event) {
	for _, sink := range service.sinks {
		if err := sink.WriteBatch(ctx, events); err != nil {
			log.Printf("logging accepted-event sink failed: %v", err)
		}
	}
}

func (service *Service) markFlushed(count int, size int64) {
	service.mu.Lock()
	service.queuedEvents -= count
	service.queuedBytes -= size
	service.setQueueDepth(service.queuedEvents)
	service.setQueueBytes(service.queuedBytes)
	service.mu.Unlock()
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

func (service *Service) setQueueBytes(size int64) {
	if service.config.Observer != nil {
		service.config.Observer.SetQueueBytes(size)
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
