package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type ingestRequest struct {
	Event sharedlogging.Event `json:"event"`
}

type batchIngestRequest struct {
	Events []sharedlogging.Event `json:"events"`
}

func (request *ingestRequest) UnmarshalJSON(payload []byte) error {
	var wire struct {
		Event json.RawMessage `json:"event"`
	}
	if err := decodeDTO(payload, &wire); err != nil {
		return err
	}
	if len(wire.Event) == 0 {
		return errors.New("event is required")
	}
	event, err := sharedlogging.DecodeEvent(wire.Event)
	if err != nil {
		return err
	}
	request.Event = event
	return nil
}

func (request *batchIngestRequest) UnmarshalJSON(payload []byte) error {
	var wire struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := decodeDTO(payload, &wire); err != nil {
		return err
	}
	if wire.Events == nil {
		return errors.New("events is required and must be an array")
	}
	request.Events = make([]sharedlogging.Event, 0, len(wire.Events))
	for _, raw := range wire.Events {
		event, err := sharedlogging.DecodeEvent(raw)
		if err != nil {
			return err
		}
		request.Events = append(request.Events, event)
	}
	return nil
}

func decodeDTO(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request must contain one JSON value")
		}
		return err
	}
	return nil
}
