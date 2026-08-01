package httpapi

import sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"

type ingestRequest struct {
	Event sharedlogging.Event `json:"event"`
}

type batchIngestRequest struct {
	Events []sharedlogging.Event `json:"events"`
}
