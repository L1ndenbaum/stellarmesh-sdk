package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

const defaultReadinessTimeout = 2 * time.Second

type healthPolicy struct {
	service       string
	livePath      string
	readyPath     string
	checkTimeout  time.Duration
	readiness     ReadinessChecker
	logSuccessful bool
	responder     HealthResponder
}

func newHealthPolicy(config *HealthConfig) (*healthPolicy, error) {
	if config == nil {
		return nil, nil
	}
	service := strings.TrimSpace(config.Service)
	if service == "" {
		service = "gateway"
	}
	livePath := strings.TrimSpace(config.LivePath)
	if livePath == "" {
		livePath = "/health/live"
	}
	readyPath := strings.TrimSpace(config.ReadyPath)
	if readyPath == "" {
		readyPath = "/health/ready"
	}
	if !strings.HasPrefix(livePath, "/") || !strings.HasPrefix(readyPath, "/") || livePath == readyPath {
		return nil, errors.New("gateway health paths must be distinct absolute paths")
	}
	checkTimeout := config.CheckTimeout
	if checkTimeout == 0 {
		checkTimeout = defaultReadinessTimeout
	}
	if checkTimeout < 0 {
		return nil, errors.New("gateway readiness timeout cannot be negative")
	}
	readiness := config.Readiness
	if isNilInterface(readiness) {
		readiness = nil
	}
	responder := config.Responder
	if responder == nil {
		responder = defaultHealthResponder{}
	} else if isNilInterface(responder) {
		return nil, errors.New("gateway health responder is nil")
	}
	return &healthPolicy{
		service: service, livePath: livePath, readyPath: readyPath,
		checkTimeout: checkTimeout, readiness: readiness, logSuccessful: config.LogSuccessful,
		responder: responder,
	}, nil
}

func (policy *healthPolicy) handle(w http.ResponseWriter, r *http.Request, gateway *Gateway, state *accessLogState) bool {
	if r.URL.Path != policy.livePath && r.URL.Path != policy.readyPath {
		return false
	}
	state.AuthResult = "public"
	state.Upstream = ""
	if r.URL.Path == policy.livePath {
		state.Route = "health_live"
	} else {
		state.Route = "health_ready"
	}
	if r.Method != http.MethodGet {
		gateway.fail(w, r, GatewayError{Status: http.StatusMethodNotAllowed, Code: "health_method_not_allowed", Message: "method not allowed"})
		return true
	}
	if r.URL.Path == policy.readyPath && policy.readiness != nil {
		ctx, cancel := context.WithTimeout(r.Context(), policy.checkTimeout)
		err := policy.readiness.Check(ctx)
		cancel()
		if err != nil {
			gateway.fail(w, r, unavailableError("readiness_failed", err))
			return true
		}
	}
	state.SkipSuccessful = !policy.logSuccessful
	kind := HealthKindLive
	if r.URL.Path == policy.readyPath {
		kind = HealthKindReady
	}
	policy.responder.RespondHealth(w, r, HealthResult{Kind: kind, Service: policy.service})
	return true
}
