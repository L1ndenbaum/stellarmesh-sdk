// Package api contains the shared HTTP JSON contract and helpers.
package api

// Envelope is the response shape shared by Stellarmesh services.
type Envelope struct {
	Code        int    `json:"code"`
	Message     string `json:"message"`
	Data        any    `json:"data"`
	Timestamp   string `json:"timestamp"`
	ErrorReason string `json:"error_reason,omitempty"`
}
