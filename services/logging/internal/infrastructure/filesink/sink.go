// Package filesink provides bounded segmented Kafka fallback replay.
package filesink

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	defaultMaxRecordBytes = sharedlogging.MaxEventJSONBytesV1 + 1
	segmentSuffix         = ".ready.jsonl"
	stagingDirectory      = ".staging"
	batchesDirectory      = "batches"
	quarantineDirectory   = "quarantine"
)

var (
	// ErrSpoolFull indicates that accepting another batch would exceed the disk budget.
	ErrSpoolFull = errors.New("logging fallback spool is full")
	// ErrRecordTooLarge indicates a corrupt or unsupported spool record.
	ErrRecordTooLarge = errors.New("logging fallback spool record is too large")
	// ErrCorruptSegment identifies a segment that cannot be decoded safely.
	ErrCorruptSegment = errors.New("logging fallback spool segment is corrupt")
)

// Publisher replays recovered events to the event bus.
type Publisher interface {
	Publish(context.Context, []sharedlogging.Event) error
}

// CheckedPublisher verifies Kafka availability before replaying committed segments.
type CheckedPublisher interface {
	Publisher
	Check(context.Context) error
}

// Observer receives bounded spool metrics.
type Observer interface {
	SetSpoolBytes(priority string, size int64)
	ObserveSpoolWrite(priority, result string, count int)
	ObserveSpoolReplay(priority, result string, count int)
}

// Config controls segmented fallback storage.
type Config struct {
	RootDir                 string
	MaxBytes                int64
	SegmentBytes            int64
	ReplayBatchSize         int
	PublishTimeout          time.Duration
	IsPermanentPublishError func(error) bool
	Observer                Observer
}

type segment struct {
	payload []byte
	events  int
}

type quarantineMetadata struct {
	OriginalPath  string    `json:"original_path"`
	Priority      string    `json:"priority"`
	Reason        string    `json:"reason"`
	QuarantinedAt time.Time `json:"quarantined_at"`
}

// KafkaFallbackStore separates regular and error/audit events into atomic segments.
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
	rename                  func(string, string) error
}

// NewKafkaFallbackStore validates directories and recovers existing segment sizes.
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
		observer:                config.Observer, rename: os.Rename,
	}
	regularBytes, err := prepareDirectory(store.directory(regularPriority))
	if err != nil {
		return nil, err
	}
	priorityBytes, err := prepareDirectory(store.directory(highPriority))
	if err != nil {
		return nil, err
	}
	batchRegularBytes, batchPriorityBytes, err := prepareBatchDirectories(store.rootDir)
	if err != nil {
		return nil, err
	}
	quarantineBytes, err := prepareQuarantineDirectory(filepath.Join(store.rootDir, quarantineDirectory))
	if err != nil {
		return nil, err
	}
	store.regularBytes = regularBytes
	store.regularBytes += batchRegularBytes
	store.priorityBytes = priorityBytes + batchPriorityBytes
	store.quarantineBytes = quarantineBytes
	store.observeBytes()
	return store, nil
}

// WriteBatch atomically segments events that failed Kafka publication.
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
	if regularSize+prioritySize > store.maxBytes-store.totalBytesLocked() {
		store.observeWrite(regularPriority, "rejected", len(regular))
		store.observeWrite(highPriority, "rejected", len(priority))
		return ErrSpoolFull
	}
	committed, err := store.commitBatchLocked(ctx, regularSegments, prioritySegments)
	if committed {
		store.regularBytes += regularSize
		store.priorityBytes += prioritySize
		store.observeBytes()
	}
	if err != nil {
		return err
	}
	store.observeWrite(regularPriority, "stored", len(regular))
	store.observeWrite(highPriority, "stored", len(priority))
	return nil
}

// ReplayOnce replays priority segments first and removes only fully published segments.
func (store *KafkaFallbackStore) ReplayOnce(ctx context.Context, publisher Publisher) error {
	if publisher == nil {
		return nil
	}
	store.replayMu.Lock()
	defer store.replayMu.Unlock()
	priorityErr := store.replayPriority(ctx, highPriority, publisher)
	regularErr := store.replayPriority(ctx, regularPriority, publisher)
	return errors.Join(priorityErr, regularErr)
}

// StartReplay periodically retries fallback delivery and reports failures or released space.
func (store *KafkaFallbackStore) StartReplay(
	ctx context.Context,
	publisher CheckedPublisher,
	interval time.Duration,
	checkTimeout time.Duration,
	onResult func(error),
) <-chan struct{} {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if checkTimeout <= 0 {
		checkTimeout = 5 * time.Second
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
				err := publisher.Check(checkCtx)
				cancel()
				if err == nil {
					err = store.ReplayOnce(ctx, publisher)
				}
				if onResult != nil {
					onResult(err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return done
}

// Saturated reports whether retained segments have exhausted the configured budget.
func (store *KafkaFallbackStore) Saturated() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.totalBytesLocked() >= store.maxBytes
}

// Bytes reports retained regular and priority bytes.
func (store *KafkaFallbackStore) Bytes() (int64, int64) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.regularBytes, store.priorityBytes
}

// QuarantineBytes reports bytes retained for operator inspection.
func (store *KafkaFallbackStore) QuarantineBytes() int64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.quarantineBytes
}

func (store *KafkaFallbackStore) totalBytesLocked() int64 {
	return store.regularBytes + store.priorityBytes + store.quarantineBytes
}

func (store *KafkaFallbackStore) encodeSegments(events []sharedlogging.Event) ([]segment, int64, error) {
	if len(events) == 0 {
		return nil, 0, nil
	}
	segments := make([]segment, 0, 1)
	current := segment{payload: make([]byte, 0, int(min(store.segmentBytes, 1<<20)))}
	var total int64
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			return nil, 0, err
		}
		payload = append(payload, '\n')
		if len(payload) > defaultMaxRecordBytes {
			return nil, 0, ErrRecordTooLarge
		}
		if current.events > 0 && int64(len(current.payload)+len(payload)) > store.segmentBytes {
			segments = append(segments, current)
			current = segment{payload: make([]byte, 0, int(min(store.segmentBytes, 1<<20)))}
		}
		current.payload = append(current.payload, payload...)
		current.events++
		total += int64(len(payload))
	}
	if current.events > 0 {
		segments = append(segments, current)
	}
	return segments, total, nil
}

func (store *KafkaFallbackStore) commitBatchLocked(
	ctx context.Context,
	regular []segment,
	priority []segment,
) (committedBatch bool, result error) {
	store.sequence++
	name := fmt.Sprintf("%020d-%06d", time.Now().UTC().UnixNano(), store.sequence)
	stagingRoot := filepath.Join(store.rootDir, stagingDirectory)
	batchRoot := filepath.Join(store.rootDir, batchesDirectory)
	staged := filepath.Join(stagingRoot, name)
	committed := filepath.Join(batchRoot, name)
	if err := os.Mkdir(staged, 0o700); err != nil {
		return false, err
	}
	defer func() {
		if result != nil {
			_ = os.RemoveAll(staged)
		}
	}()
	for _, group := range []struct {
		priority string
		segments []segment
	}{
		{priority: regularPriority, segments: regular},
		{priority: highPriority, segments: priority},
	} {
		if len(group.segments) == 0 {
			continue
		}
		directory := filepath.Join(staged, group.priority)
		if err := os.Mkdir(directory, 0o700); err != nil {
			return false, err
		}
		if err := writeStagedSegments(directory, group.segments); err != nil {
			return false, err
		}
		if err := syncDirectory(directory); err != nil {
			return false, err
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := syncDirectory(staged); err != nil {
		return false, err
	}
	if err := store.rename(staged, committed); err != nil {
		return false, err
	}
	committedBatch = true
	if err := syncDirectory(batchRoot); err != nil {
		return true, err
	}
	return true, syncDirectory(stagingRoot)
}

func writeStagedSegments(directory string, segments []segment) error {
	for index, item := range segments {
		path := filepath.Join(directory, fmt.Sprintf("%06d%s", index+1, segmentSuffix))
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		writeErr := writeAndSync(file, item.payload)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return errors.Join(writeErr, closeErr)
		}
	}
	return nil
}

func (store *KafkaFallbackStore) replayPriority(ctx context.Context, priority string, publisher Publisher) error {
	paths, err := store.committedSegmentPaths(priority)
	if err != nil {
		return err
	}
	for _, path := range paths {
		published, failed, err := store.replaySegment(ctx, path, publisher)
		if err != nil {
			if errors.Is(err, ErrCorruptSegment) || store.isPermanentPublishFailure(err) {
				if quarantineErr := store.quarantineSegment(path, priority, err); quarantineErr != nil {
					store.observeReplay(priority, "quarantine_failed", max(failed, 1))
					return fmt.Errorf("quarantine logging spool segment %s: %w", filepath.Base(path), quarantineErr)
				}
				store.observeReplay(priority, "quarantined", max(failed, 1))
				continue
			}
			store.observeReplay(priority, "failed", max(failed, 1))
			return fmt.Errorf("replay logging spool segment %s: %w", filepath.Base(path), err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		store.mu.Lock()
		if priority == highPriority {
			store.priorityBytes -= info.Size()
		} else {
			store.regularBytes -= info.Size()
		}
		store.observeBytes()
		store.mu.Unlock()
		store.observeReplay(priority, "published", published)
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
	}
	return store.removeEmptyBatches()
}

func (store *KafkaFallbackStore) isPermanentPublishFailure(err error) bool {
	return store.isPermanentPublishError != nil && store.isPermanentPublishError(err)
}

func (store *KafkaFallbackStore) quarantineSegment(path, priority string, cause error) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	relativePath, err := filepath.Rel(store.rootDir, path)
	if err != nil {
		return err
	}
	store.mu.Lock()
	store.sequence++
	name := fmt.Sprintf("%020d-%06d", time.Now().UTC().UnixNano(), store.sequence)
	store.mu.Unlock()

	directory := filepath.Join(store.rootDir, quarantineDirectory)
	target := filepath.Join(directory, name+segmentSuffix)
	metadataPath := filepath.Join(directory, name+".json")
	temporaryMetadata := metadataPath + ".tmp"
	metadata, err := json.Marshal(quarantineMetadata{
		OriginalPath: relativePath, Priority: priority,
		Reason: truncateError(cause.Error(), 2048), QuarantinedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	metadata = append(metadata, '\n')
	file, err := os.OpenFile(temporaryMetadata, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	writeErr := writeAndSync(file, metadata)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporaryMetadata)
		return errors.Join(writeErr, closeErr)
	}
	if err := store.rename(path, target); err != nil {
		_ = os.Remove(temporaryMetadata)
		return err
	}
	metadataErr := store.rename(temporaryMetadata, metadataPath)
	if metadataErr != nil {
		_ = os.Remove(temporaryMetadata)
	}

	store.mu.Lock()
	if priority == highPriority {
		store.priorityBytes -= info.Size()
	} else {
		store.regularBytes -= info.Size()
	}
	store.quarantineBytes += info.Size()
	if metadataErr == nil {
		store.quarantineBytes += int64(len(metadata))
	}
	store.observeBytes()
	store.mu.Unlock()
	return errors.Join(metadataErr, syncDirectory(directory), syncDirectory(filepath.Dir(path)))
}

func truncateError(value string, limit int) string {
	if len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func (store *KafkaFallbackStore) replaySegment(ctx context.Context, path string, publisher Publisher) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	batch := make([]sharedlogging.Event, 0, store.replayBatchSize)
	published := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		publishCtx, cancel := context.WithTimeout(ctx, store.publishTimeout)
		err := publisher.Publish(publishCtx, batch)
		cancel()
		if err != nil {
			return err
		}
		published += len(batch)
		batch = batch[:0]
		return nil
	}
	for {
		record, err := readRecord(reader, defaultMaxRecordBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return published, len(batch), fmt.Errorf("%w: %v", ErrCorruptSegment, err)
		}
		if len(bytes.TrimSpace(record)) == 0 {
			continue
		}
		event, err := sharedlogging.DecodeEvent(record)
		if err != nil {
			return published, len(batch), fmt.Errorf("%w: %v", ErrCorruptSegment, err)
		}
		batch = append(batch, event)
		if len(batch) >= store.replayBatchSize {
			if err := flush(); err != nil {
				return published, len(batch), err
			}
		}
	}
	if err := flush(); err != nil {
		return published, len(batch), err
	}
	return published, 0, nil
}

func readRecord(reader *bufio.Reader, limit int) ([]byte, error) {
	var record []byte
	for {
		part, prefix, err := reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && len(record) > 0 {
				return record, nil
			}
			return nil, err
		}
		if len(record)+len(part) > limit {
			return nil, ErrRecordTooLarge
		}
		record = append(record, part...)
		if !prefix {
			return record, nil
		}
	}
}

func partitionEvents(events []sharedlogging.Event) ([]sharedlogging.Event, []sharedlogging.Event) {
	regular := make([]sharedlogging.Event, 0, len(events))
	priority := make([]sharedlogging.Event, 0, len(events))
	for _, event := range events {
		if event.Level == sharedlogging.LevelError || event.Level == sharedlogging.LevelAudit {
			priority = append(priority, event)
		} else {
			regular = append(regular, event)
		}
	}
	return regular, priority
}

func prepareDirectory(path string) (int64, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return 0, err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}
	var size int64
	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		if strings.HasSuffix(entry.Name(), ".tmp") {
			if err := os.Remove(fullPath); err != nil {
				return 0, err
			}
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), segmentSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, err
		}
		size += info.Size()
	}
	return size, nil
}

func prepareBatchDirectories(root string) (int64, int64, error) {
	staging := filepath.Join(root, stagingDirectory)
	batches := filepath.Join(root, batchesDirectory)
	for _, directory := range []string{staging, batches} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return 0, 0, err
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return 0, 0, err
		}
	}
	staged, err := os.ReadDir(staging)
	if err != nil {
		return 0, 0, err
	}
	for _, entry := range staged {
		if err := os.RemoveAll(filepath.Join(staging, entry.Name())); err != nil {
			return 0, 0, err
		}
	}
	entries, err := os.ReadDir(batches)
	if err != nil {
		return 0, 0, err
	}
	var regularBytes int64
	var priorityBytes int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		batch := filepath.Join(batches, entry.Name())
		regular, err := prepareDirectory(filepath.Join(batch, regularPriority))
		if err != nil {
			return 0, 0, err
		}
		priority, err := prepareDirectory(filepath.Join(batch, highPriority))
		if err != nil {
			return 0, 0, err
		}
		regularBytes += regular
		priorityBytes += priority
	}
	return regularBytes, priorityBytes, nil
}

func prepareQuarantineDirectory(path string) (int64, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return 0, err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}
	var size int64
	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		if strings.HasSuffix(entry.Name(), ".tmp") {
			if err := os.Remove(fullPath); err != nil {
				return 0, err
			}
			continue
		}
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, err
		}
		size += info.Size()
	}
	return size, nil
}

func (store *KafkaFallbackStore) committedSegmentPaths(priority string) ([]string, error) {
	paths, err := segmentPaths(store.directory(priority))
	if err != nil {
		return nil, err
	}
	batches := filepath.Join(store.rootDir, batchesDirectory)
	entries, err := os.ReadDir(batches)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		batchPaths, err := segmentPaths(filepath.Join(batches, entry.Name(), priority))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		paths = append(paths, batchPaths...)
	}
	return paths, nil
}

func (store *KafkaFallbackStore) removeEmptyBatches() error {
	batches := filepath.Join(store.rootDir, batchesDirectory)
	entries, err := os.ReadDir(batches)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		batch := filepath.Join(batches, entry.Name())
		empty, err := emptyCommittedBatch(batch)
		if err != nil {
			return err
		}
		if !empty {
			continue
		}
		if err := os.RemoveAll(batch); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return syncDirectory(batches)
	}
	return nil
}

func emptyCommittedBatch(batch string) (bool, error) {
	entries, err := os.ReadDir(batch)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || (entry.Name() != regularPriority && entry.Name() != highPriority) {
			return false, nil
		}
		children, err := os.ReadDir(filepath.Join(batch, entry.Name()))
		if err != nil {
			return false, err
		}
		if len(children) > 0 {
			return false, nil
		}
	}
	return true, nil
}

func segmentPaths(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), segmentSuffix) {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func writeAndSync(file *os.File, payload []byte) error {
	written, err := file.Write(payload)
	if err != nil {
		return err
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	return file.Sync()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
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
