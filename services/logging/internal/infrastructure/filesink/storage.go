package filesink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type segment struct {
	payload []byte
	events  int
}

type segmentStats struct {
	bytes int64
	count int64
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
		if err := store.syncDir(directory); err != nil {
			return false, err
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.syncDir(staged); err != nil {
		return false, err
	}
	if err := store.rename(staged, committed); err != nil {
		return false, err
	}
	committedBatch = true
	if err := store.syncDir(batchRoot); err != nil {
		return true, err
	}
	return true, store.syncDir(stagingRoot)
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

func prepareDirectory(path string) (segmentStats, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return segmentStats{}, err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return segmentStats{}, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return segmentStats{}, err
	}
	var stats segmentStats
	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		if strings.HasSuffix(entry.Name(), ".tmp") {
			if err := os.Remove(fullPath); err != nil {
				return segmentStats{}, err
			}
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), segmentSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return segmentStats{}, err
		}
		stats.bytes += info.Size()
		stats.count++
	}
	return stats, nil
}

func prepareBatchDirectories(root string) (segmentStats, segmentStats, error) {
	staging := filepath.Join(root, stagingDirectory)
	batches := filepath.Join(root, batchesDirectory)
	for _, directory := range []string{staging, batches} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return segmentStats{}, segmentStats{}, err
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return segmentStats{}, segmentStats{}, err
		}
	}
	staged, err := os.ReadDir(staging)
	if err != nil {
		return segmentStats{}, segmentStats{}, err
	}
	for _, entry := range staged {
		if err := os.RemoveAll(filepath.Join(staging, entry.Name())); err != nil {
			return segmentStats{}, segmentStats{}, err
		}
	}
	entries, err := os.ReadDir(batches)
	if err != nil {
		return segmentStats{}, segmentStats{}, err
	}
	var regularStats segmentStats
	var priorityStats segmentStats
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		batch := filepath.Join(batches, entry.Name())
		regular, err := prepareDirectory(filepath.Join(batch, regularPriority))
		if err != nil {
			return segmentStats{}, segmentStats{}, err
		}
		priority, err := prepareDirectory(filepath.Join(batch, highPriority))
		if err != nil {
			return segmentStats{}, segmentStats{}, err
		}
		regularStats.bytes += regular.bytes
		regularStats.count += regular.count
		priorityStats.bytes += priority.bytes
		priorityStats.count += priority.count
	}
	return regularStats, priorityStats, nil
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
