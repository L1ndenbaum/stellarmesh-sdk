package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type fakeInserter struct {
	err    error
	events []sharedlogging.Event
}

func (inserter *fakeInserter) InsertEvents(_ context.Context, events []sharedlogging.Event) error {
	inserter.events = append(inserter.events, events...)
	return inserter.err
}

type fakeCommitter struct {
	committed bool
}

func (committer *fakeCommitter) Commit(context.Context, []Message) error {
	committer.committed = true
	return nil
}

func TestProcessBatchCommitsAfterInsert(t *testing.T) {
	payload, err := json.Marshal(validEvent(t))
	if err != nil {
		t.Fatal(err)
	}
	inserter := &fakeInserter{}
	committer := &fakeCommitter{}
	if err := ProcessBatch(context.Background(), []Message{{Value: payload}}, inserter, committer); err != nil {
		t.Fatal(err)
	}
	if len(inserter.events) != 1 || !committer.committed {
		t.Fatalf("events=%d committed=%v", len(inserter.events), committer.committed)
	}
}

func TestProcessBatchDoesNotCommitFailedInsert(t *testing.T) {
	payload, _ := json.Marshal(validEvent(t))
	committer := &fakeCommitter{}
	err := ProcessBatch(context.Background(), []Message{{Value: payload}}, &fakeInserter{err: errors.New("unavailable")}, committer)
	if err == nil || committer.committed {
		t.Fatalf("error=%v committed=%v", err, committer.committed)
	}
}

func validEvent(t *testing.T) sharedlogging.Event {
	t.Helper()
	id, err := sharedlogging.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	return sharedlogging.Event{
		EventID: id, Timestamp: time.Now(), Level: sharedlogging.LevelInfo,
		Service: "test", Message: "event", Metadata: map[string]any{},
	}
}
