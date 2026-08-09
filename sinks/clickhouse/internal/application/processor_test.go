package application

import (
	"context"
	"encoding/base64"
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

type fakeDeadLetterPublisher struct {
	err     error
	records []sharedlogging.DeadLetter
}

func (publisher *fakeDeadLetterPublisher) PublishDeadLetters(
	_ context.Context,
	records []sharedlogging.DeadLetter,
) error {
	publisher.records = append(publisher.records, records...)
	return publisher.err
}

type fakeCommitter struct {
	err      error
	messages []Message
}

func (committer *fakeCommitter) Commit(_ context.Context, messages []Message) error {
	committer.messages = append(committer.messages, messages...)
	return committer.err
}

func TestProcessorInsertsValidDeadLettersInvalidThenCommitsAll(t *testing.T) {
	payload, err := json.Marshal(validEvent(t))
	if err != nil {
		t.Fatal(err)
	}
	inserter := &fakeInserter{}
	deadLetters := &fakeDeadLetterPublisher{}
	committer := &fakeCommitter{}
	processor := newTestProcessor(t, inserter, deadLetters, committer)
	messages := []Message{
		validSourceMessage(payload, 10),
		validSourceMessage([]byte(`{"unknown":true}`), 11),
	}
	if err := processor.ProcessBatch(context.Background(), messages); err != nil {
		t.Fatal(err)
	}
	if len(inserter.events) != 1 || len(deadLetters.records) != 1 || len(committer.messages) != 2 {
		t.Fatalf("events=%d dead_letters=%d committed=%d", len(inserter.events), len(deadLetters.records), len(committer.messages))
	}
	record := deadLetters.records[0]
	decoded, err := base64.StdEncoding.DecodeString(record.PayloadBase64)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != `{"unknown":true}` || record.SourceOffset != 11 {
		t.Fatalf("dead letter = %#v", record)
	}
}

func TestProcessorDoesNotDeadLetterOrCommitFailedInsert(t *testing.T) {
	payload, _ := json.Marshal(validEvent(t))
	deadLetters := &fakeDeadLetterPublisher{}
	committer := &fakeCommitter{}
	processor := newTestProcessor(t, &fakeInserter{err: errors.New("unavailable")}, deadLetters, committer)
	err := processor.ProcessBatch(context.Background(), []Message{validSourceMessage(payload, 1)})
	if !errors.Is(err, ErrClickHouseInsert) || len(deadLetters.records) != 0 || len(committer.messages) != 0 {
		t.Fatalf("error=%v dead_letters=%d committed=%d", err, len(deadLetters.records), len(committer.messages))
	}
}

func TestProcessorDoesNotCommitFailedDeadLetterPublish(t *testing.T) {
	committer := &fakeCommitter{}
	processor := newTestProcessor(t, &fakeInserter{}, &fakeDeadLetterPublisher{err: errors.New("unavailable")}, committer)
	err := processor.ProcessBatch(context.Background(), []Message{validSourceMessage([]byte("invalid"), 1)})
	if !errors.Is(err, ErrDeadLetterPublish) || len(committer.messages) != 0 {
		t.Fatalf("error=%v committed=%d", err, len(committer.messages))
	}
}

func TestProcessorReportsFailedCommit(t *testing.T) {
	payload, _ := json.Marshal(validEvent(t))
	processor := newTestProcessor(t, &fakeInserter{}, &fakeDeadLetterPublisher{}, &fakeCommitter{err: errors.New("unavailable")})
	err := processor.ProcessBatch(context.Background(), []Message{validSourceMessage(payload, 1)})
	if !errors.Is(err, ErrOffsetCommit) {
		t.Fatalf("error = %v", err)
	}
}

func newTestProcessor(
	t *testing.T,
	inserter Inserter,
	deadLetters DeadLetterPublisher,
	committer Committer,
) *Processor {
	t.Helper()
	processor, err := NewProcessor(ProcessorConfig{
		Inserter: inserter, DeadLetters: deadLetters, Committer: committer,
		Now: func() time.Time { return time.Date(2026, 8, 1, 12, 0, 1, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

func validSourceMessage(payload []byte, offset int64) Message {
	return Message{
		Topic: sharedlogging.TopicV1, Partition: 1, Offset: offset,
		Timestamp: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Key:       []byte("trace-123"), Value: payload,
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
