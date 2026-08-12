package filesink

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

// ReplayOnce 独立回放 priority 和 regular 分段，并隔离永久失败记录。
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

// StartReplay 定期重试 fallback 投递，并报告失败或已释放空间。
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

func (store *KafkaFallbackStore) replayPriority(ctx context.Context, priority string, publisher Publisher) error {
	paths, err := store.committedSegmentPaths(priority)
	if err != nil {
		return err
	}
	for _, path := range paths {
		published, rejected, failed, err := store.replaySegment(ctx, path, publisher)
		if err != nil {
			if errors.Is(err, ErrCorruptSegment) {
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
		if len(rejected) > 0 {
			if err := store.quarantineRecords(path, priority, rejected); err != nil {
				store.observeReplay(priority, "quarantine_failed", len(rejected))
				return fmt.Errorf("quarantine rejected logging records from %s: %w", filepath.Base(path), err)
			}
			store.observeReplay(priority, "quarantined", len(rejected))
		} else {
			if err := store.removePublishedSegment(path, priority); err != nil {
				return err
			}
		}
		store.observeReplay(priority, "published", published)
	}
	return store.removeEmptyBatches()
}

func (store *KafkaFallbackStore) isPermanentPublishFailure(err error) bool {
	return store.isPermanentPublishError != nil && store.isPermanentPublishError(err)
}

type rejectedRecord struct {
	event sharedlogging.Event
	cause error
}

func (store *KafkaFallbackStore) replaySegment(
	ctx context.Context,
	path string,
	publisher Publisher,
) (int, []rejectedRecord, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, nil, 0, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	batch := make([]sharedlogging.Event, 0, store.replayBatchSize)
	published := 0
	rejected := make([]rejectedRecord, 0)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		accepted, failedRecords, err := store.publishReplayBatch(ctx, publisher, batch)
		if err != nil {
			return err
		}
		published += accepted
		rejected = append(rejected, failedRecords...)
		batch = batch[:0]
		return nil
	}
	for {
		record, err := readRecord(reader, defaultMaxRecordBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return published, rejected, len(batch), fmt.Errorf("%w: %v", ErrCorruptSegment, err)
		}
		if len(bytes.TrimSpace(record)) == 0 {
			continue
		}
		event, err := sharedlogging.DecodeEvent(record)
		if err != nil {
			return published, rejected, len(batch), fmt.Errorf("%w: %v", ErrCorruptSegment, err)
		}
		batch = append(batch, event)
		if len(batch) >= store.replayBatchSize {
			if err := flush(); err != nil {
				return published, rejected, len(batch), err
			}
		}
	}
	if err := flush(); err != nil {
		return published, rejected, len(batch), err
	}
	return published, rejected, 0, nil
}

func (store *KafkaFallbackStore) publishReplayBatch(
	ctx context.Context,
	publisher Publisher,
	events []sharedlogging.Event,
) (int, []rejectedRecord, error) {
	publishCtx, cancel := context.WithTimeout(ctx, store.publishTimeout)
	err := publisher.Publish(publishCtx, events)
	cancel()
	if err == nil {
		return len(events), nil, nil
	}
	if !store.isPermanentPublishFailure(err) {
		return 0, nil, err
	}
	if len(events) == 1 {
		return 0, []rejectedRecord{{event: events[0], cause: err}}, nil
	}
	middle := len(events) / 2
	leftPublished, leftRejected, err := store.publishReplayBatch(ctx, publisher, events[:middle])
	if err != nil {
		return leftPublished, leftRejected, err
	}
	rightPublished, rightRejected, err := store.publishReplayBatch(ctx, publisher, events[middle:])
	return leftPublished + rightPublished, append(leftRejected, rightRejected...), err
}

func (store *KafkaFallbackStore) removePublishedSegment(path, priority string) error {
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
	store.releaseQuarantineReserveLocked(info.Size())
	store.observeBytes()
	store.mu.Unlock()
	return store.syncDir(filepath.Dir(path))
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
		return store.syncDir(batches)
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
