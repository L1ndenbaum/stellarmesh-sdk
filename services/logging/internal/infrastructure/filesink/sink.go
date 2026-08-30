// Package filesink 提供有容量上限的 Kafka fallback 分段与回放。
package filesink

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

const (
	regularPriority       = "regular"
	highPriority          = "priority"
	defaultMaxBytes       = int64(1 << 30)
	defaultSegmentBytes   = int64(16 << 20)
	defaultReplayBatch    = 128
	defaultMaxRecordBytes = sharedlogging.MaxEventJSONBytesV2 + 1
	// 覆盖隔离产物旁保存的有界原因、源路径、时间戳和 JSON envelope。
	quarantineMetadataReserveBytes = int64(64 << 10)
	segmentSuffix                  = ".ready.jsonl"
	formatFileName                 = "FORMAT"
	spoolFormatV2                  = "stellarmesh-logging-spool-v2\n"
	stagingDirectory               = ".staging"
	batchesDirectory               = "batches"
	quarantineDirectory            = "quarantine"
)

var (
	// ErrSpoolFull 表示再接收一个批次将超过磁盘预算。
	ErrSpoolFull = errors.New("logging fallback spool is full")
	// ErrRecordTooLarge 表示 spool 记录已损坏或不受支持。
	ErrRecordTooLarge = errors.New("logging fallback spool record is too large")
	// ErrCorruptSegment 标识无法安全解码的分段。
	ErrCorruptSegment = errors.New("logging fallback spool segment is corrupt")
	// ErrIncompatibleSpool 标识数据目录不是当前 v2 spool 格式。
	ErrIncompatibleSpool = errors.New("logging fallback spool format is incompatible")
)

// Publisher 将恢复的事件回放到事件总线。
type Publisher interface {
	Publish(context.Context, []sharedlogging.Event) error
}

// CheckedPublisher 在回放已提交分段前校验 Kafka 可用性。
type CheckedPublisher interface {
	Publisher
	Check(context.Context) error
}

// Observer 接收有界 spool 指标。
type Observer interface {
	SetSpoolBytes(priority string, size int64)
	ObserveSpoolWrite(priority, result string, count int)
	ObserveSpoolReplay(priority, result string, count int)
}

// Config 控制分段 fallback 存储。
type Config struct {
	RootDir                 string
	MaxBytes                int64
	SegmentBytes            int64
	ReplayBatchSize         int
	PublishTimeout          time.Duration
	IsPermanentPublishError func(error) bool
	Observer                Observer
}

// KafkaFallbackStore 将普通事件和 error 或 audit 事件拆分到原子分段中。
type KafkaFallbackStore struct {
	rootDir                 string
	maxBytes                int64
	segmentBytes            int64
	replayBatchSize         int
	publishTimeout          time.Duration
	isPermanentPublishError func(error) bool
	observer                Observer
	mu                      sync.Mutex
	replayMu                sync.Mutex
	sequence                uint64
	regularBytes            int64
	priorityBytes           int64
	quarantineBytes         int64
	quarantineReserveBytes  int64
	rename                  func(string, string) error
	syncDir                 func(string) error
}

// NewKafkaFallbackStore 校验目录并恢复既有分段大小。
func NewKafkaFallbackStore(config Config) (*KafkaFallbackStore, error) {
	if strings.TrimSpace(config.RootDir) == "" {
		return nil, errors.New("logging fallback spool directory is required")
	}
	if config.MaxBytes <= 0 {
		config.MaxBytes = defaultMaxBytes
	}
	if config.SegmentBytes <= 0 {
		config.SegmentBytes = defaultSegmentBytes
	}
	if config.ReplayBatchSize <= 0 {
		config.ReplayBatchSize = defaultReplayBatch
	}
	if config.PublishTimeout <= 0 {
		config.PublishTimeout = 5 * time.Second
	}
	store := &KafkaFallbackStore{
		rootDir: config.RootDir, maxBytes: config.MaxBytes, segmentBytes: config.SegmentBytes,
		replayBatchSize: config.ReplayBatchSize, publishTimeout: config.PublishTimeout,
		isPermanentPublishError: config.IsPermanentPublishError,
		observer:                config.Observer, rename: os.Rename, syncDir: syncDirectory,
	}
	if err := ensureSpoolFormat(store.rootDir); err != nil {
		return nil, err
	}
	regular, err := prepareDirectory(store.directory(regularPriority))
	if err != nil {
		return nil, err
	}
	priority, err := prepareDirectory(store.directory(highPriority))
	if err != nil {
		return nil, err
	}
	batchRegular, batchPriority, err := prepareBatchDirectories(store.rootDir)
	if err != nil {
		return nil, err
	}
	quarantineBytes, err := prepareQuarantineDirectory(filepath.Join(store.rootDir, quarantineDirectory))
	if err != nil {
		return nil, err
	}
	store.regularBytes = regular.bytes + batchRegular.bytes
	store.priorityBytes = priority.bytes + batchPriority.bytes
	store.quarantineBytes = quarantineBytes
	liveBytes := store.regularBytes + store.priorityBytes
	liveSegments := regular.count + priority.count + batchRegular.count + batchPriority.count
	store.quarantineReserveBytes = liveBytes + liveSegments*quarantineMetadataReserveBytes
	store.observeBytes()
	return store, nil
}

// WriteBatch 将 Kafka 发布失败的事件原子写入分段。
func (store *KafkaFallbackStore) WriteBatch(ctx context.Context, events []sharedlogging.Event) error {
	if len(events) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	regular, priority := partitionEvents(events)
	regularSegments, regularSize, err := store.encodeSegments(regular)
	if err != nil {
		return err
	}
	prioritySegments, prioritySize, err := store.encodeSegments(priority)
	if err != nil {
		return err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	incomingBytes := regularSize + prioritySize
	incomingReserve := incomingBytes +
		int64(len(regularSegments)+len(prioritySegments))*quarantineMetadataReserveBytes
	if exceedsBudget(store.maxBytes, store.budgetedBytesLocked(), incomingBytes, incomingReserve) {
		store.observeWrite(regularPriority, "rejected", len(regular))
		store.observeWrite(highPriority, "rejected", len(priority))
		return ErrSpoolFull
	}
	committed, err := store.commitBatchLocked(ctx, regularSegments, prioritySegments)
	if committed {
		store.regularBytes += regularSize
		store.priorityBytes += prioritySize
		store.quarantineReserveBytes += incomingReserve
		store.observeBytes()
	}
	if err != nil {
		return err
	}
	store.observeWrite(regularPriority, "stored", len(regular))
	store.observeWrite(highPriority, "stored", len(priority))
	return nil
}

// Saturated 判断保留的分段是否已耗尽配置预算。
func (store *KafkaFallbackStore) Saturated() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.budgetedBytesLocked() >= store.maxBytes
}

// Bytes 报告保留的 regular 和 priority 字节数。
func (store *KafkaFallbackStore) Bytes() (int64, int64) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.regularBytes, store.priorityBytes
}

// QuarantineBytes 报告为运维检查保留的字节数。
func (store *KafkaFallbackStore) QuarantineBytes() int64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.quarantineBytes
}

func (store *KafkaFallbackStore) totalBytesLocked() int64 {
	return store.regularBytes + store.priorityBytes + store.quarantineBytes
}

func (store *KafkaFallbackStore) budgetedBytesLocked() int64 {
	return store.totalBytesLocked() + store.quarantineReserveBytes
}

func (store *KafkaFallbackStore) releaseQuarantineReserveLocked(segmentBytes int64) {
	release := segmentBytes + quarantineMetadataReserveBytes
	if release >= store.quarantineReserveBytes {
		store.quarantineReserveBytes = 0
		return
	}
	store.quarantineReserveBytes -= release
}

func exceedsBudget(limit int64, current int64, additions ...int64) bool {
	if current > limit {
		return true
	}
	available := limit - current
	for _, addition := range additions {
		if addition < 0 || addition > available {
			return true
		}
		available -= addition
	}
	return false
}

func (store *KafkaFallbackStore) directory(priority string) string {
	return filepath.Join(store.rootDir, priority)
}

func (store *KafkaFallbackStore) observeBytes() {
	if store.observer != nil {
		store.observer.SetSpoolBytes(regularPriority, store.regularBytes)
		store.observer.SetSpoolBytes(highPriority, store.priorityBytes)
		store.observer.SetSpoolBytes(quarantineDirectory, store.quarantineBytes)
	}
}

func (store *KafkaFallbackStore) observeWrite(priority, result string, count int) {
	if store.observer != nil && count > 0 {
		store.observer.ObserveSpoolWrite(priority, result, count)
	}
}

func (store *KafkaFallbackStore) observeReplay(priority, result string, count int) {
	if store.observer != nil && count > 0 {
		store.observer.ObserveSpoolReplay(priority, result, count)
	}
}
