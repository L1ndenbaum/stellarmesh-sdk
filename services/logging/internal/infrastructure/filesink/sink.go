// Package filesink provides local JSONL archives and Kafka fallback replay.
package filesink

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

// Publisher replays recovered events to the event bus.
type Publisher interface {
	Publish(context.Context, []sharedlogging.Event) error
}

// KafkaFallbackStore separates regular and error/audit events while Kafka is down.
type KafkaFallbackStore struct {
	regularPath    string
	errorAuditPath string
	mu             sync.Mutex
}

// NewKafkaFallbackStore creates a two-file fallback store.
func NewKafkaFallbackStore(regularPath, errorAuditPath string) *KafkaFallbackStore {
	return &KafkaFallbackStore{regularPath: regularPath, errorAuditPath: errorAuditPath}
}

// WriteBatch partitions and appends failed Kafka events.
func (store *KafkaFallbackStore) WriteBatch(ctx context.Context, events []sharedlogging.Event) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	var regular []sharedlogging.Event
	var errorAudit []sharedlogging.Event
	for _, event := range events {
		if event.Level == sharedlogging.LevelError || event.Level == sharedlogging.LevelAudit {
			errorAudit = append(errorAudit, event)
		} else {
			regular = append(regular, event)
		}
	}
	if err := appendEvents(ctx, store.regularPath, regular); err != nil {
		return err
	}
	return appendEvents(ctx, store.errorAuditPath, errorAudit)
}

// ReplayOnce publishes fallback files and truncates each successful file.
func (store *KafkaFallbackStore) ReplayOnce(ctx context.Context, publisher Publisher) error {
	if publisher == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.replayFile(ctx, store.regularPath, publisher); err != nil {
		return err
	}
	return store.replayFile(ctx, store.errorAuditPath, publisher)
}

// StartReplay periodically retries fallback delivery.
func (store *KafkaFallbackStore) StartReplay(ctx context.Context, publisher Publisher, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = store.ReplayOnce(ctx, publisher)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (store *KafkaFallbackStore) replayFile(ctx context.Context, path string, publisher Publisher) error {
	events, err := readEvents(path)
	if err != nil || len(events) == 0 {
		return err
	}
	if err := publisher.Publish(ctx, events); err != nil {
		return err
	}
	return truncateFile(path)
}

func appendEvents(ctx context.Context, path string, events []sharedlogging.Event) error {
	if len(events) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}

func readEvents(path string) ([]sharedlogging.Event, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var events []sharedlogging.Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var event sharedlogging.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, scanner.Err()
}

func truncateFile(path string) error {
	return os.WriteFile(path, nil, 0o600)
}
