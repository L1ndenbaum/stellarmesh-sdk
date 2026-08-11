package filesink

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type quarantineMetadata struct {
	OriginalPath  string    `json:"original_path"`
	Priority      string    `json:"priority"`
	Reason        string    `json:"reason"`
	QuarantinedAt time.Time `json:"quarantined_at"`
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
	return errors.Join(metadataErr, store.syncDir(directory), store.syncDir(filepath.Dir(path)))
}

func truncateError(value string, limit int) string {
	if len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
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
