// Package console 将已持久确认的事件异步输出到容器标准输出。
package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

const (
	defaultQueueEvents = 1024
	defaultQueueBytes  = int64(8 << 20)
)

// Observer 接收有限结果集合的控制台输出指标。
type Observer interface {
	ObserveConsole(result string, count int)
}

type queuedLine struct {
	payload []byte
	bytes   int64
}

// Sink 使用有界队列隔离标准输出阻塞，控制台副本不参与持久化确认。
type Sink struct {
	writer         io.Writer
	observer       Observer
	queue          chan queuedLine
	capacityEvents int
	capacityBytes  int64
	queuedEvents   int
	queuedBytes    int64
	closed         bool
	done           chan struct{}
	closeOnce      sync.Once
	mu             sync.Mutex
}

// NewSink 创建并启动异步单行 JSON sink。
func NewSink(writer io.Writer, observer Observer) (*Sink, error) {
	return newSink(writer, observer, defaultQueueEvents, defaultQueueBytes)
}

func newSink(writer io.Writer, observer Observer, capacityEvents int, capacityBytes int64) (*Sink, error) {
	if writer == nil {
		return nil, errors.New("logging console writer is required")
	}
	if capacityEvents <= 0 || capacityBytes <= 0 {
		return nil, errors.New("logging console queue limits must be positive")
	}
	sink := &Sink{
		writer: writer, observer: observer, queue: make(chan queuedLine, capacityEvents),
		capacityEvents: capacityEvents, capacityBytes: capacityBytes, done: make(chan struct{}),
	}
	go sink.run()
	return sink, nil
}

// WriteBatch 编码并尝试入队，不等待 writer 完成 I/O。
func (sink *Sink) WriteBatch(_ context.Context, events []sharedlogging.Event) error {
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			sink.observe("failed", 1)
			continue
		}
		payload = append(payload, '\n')
		sink.enqueue(payload)
	}
	return nil
}

// Close 停止接收新副本，并在调用方期限内等待队列排空。
func (sink *Sink) Close(ctx context.Context) error {
	sink.closeOnce.Do(func() {
		sink.mu.Lock()
		sink.closed = true
		close(sink.queue)
		sink.mu.Unlock()
	})
	select {
	case <-sink.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (sink *Sink) enqueue(payload []byte) {
	size := int64(len(payload))
	sink.mu.Lock()
	if sink.closed || sink.queuedEvents >= sink.capacityEvents || size > sink.capacityBytes-sink.queuedBytes {
		sink.mu.Unlock()
		sink.observe("dropped", 1)
		return
	}
	dropped := false
	select {
	case sink.queue <- queuedLine{payload: payload, bytes: size}:
		sink.queuedEvents++
		sink.queuedBytes += size
	default:
		dropped = true
	}
	sink.mu.Unlock()
	if dropped {
		sink.observe("dropped", 1)
	}
}

func (sink *Sink) run() {
	defer close(sink.done)
	for line := range sink.queue {
		written, err := sink.writer.Write(line.payload)
		if err == nil && written != len(line.payload) {
			err = io.ErrShortWrite
		}
		sink.release(line)
		if err != nil {
			sink.observe("failed", 1)
			continue
		}
		sink.observe("emitted", 1)
	}
}

func (sink *Sink) release(line queuedLine) {
	sink.mu.Lock()
	sink.queuedEvents--
	sink.queuedBytes -= line.bytes
	sink.mu.Unlock()
}

func (sink *Sink) observe(result string, count int) {
	if sink.observer != nil {
		sink.observer.ObserveConsole(result, count)
	}
}
