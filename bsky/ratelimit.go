package bsky

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bluesky-social/indigo/xrpc"
	log "github.com/mikeblum/atproto-graph-viz/conf"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	ReadOperationStr  = "READ_OP"
	WriteOperationStr = "WRITE_OP"

	ErrRepoTakedown    = "RepoTakendown"
	ErrRepoDeactivated = "RepoDeactivated"
)

type OperationType int

func (op OperationType) String() string {
	switch op {
	case WriteOperation:
		return WriteOperationStr
	default:
		return ReadOperationStr
	}
}

const (
	ReadOperation OperationType = iota
	WriteOperation
)

type RateLimitHandler struct {
	client            *xrpc.Client
	conf              *Conf
	log               *log.Log
	maxRetries        int
	maxWaitTime       time.Duration
	readBaseWaitTime  time.Duration
	writeBaseWaitTime time.Duration
	metrics           *RateLimitMetrics
}

// NewRateLimitHandler - default rate limt
func NewRateLimitHandler(client *xrpc.Client) *RateLimitHandler {
	conf := NewConf()
	return &RateLimitHandler{
		client:            client,
		conf:              conf,
		log:               log.NewLog(),
		maxRetries:        conf.MaxRetries(),
		maxWaitTime:       30 * time.Second,       // 30s retry deadline
		readBaseWaitTime:  500 * time.Millisecond, // 500ms for read operations
		writeBaseWaitTime: 1 * time.Second,        // 1s for write operations
	}
}

// executeWithRetry executes an API call with rate limit handling
func (h *RateLimitHandler) withRetry(ctx context.Context, opType OperationType, opName string, operation func() error) error {
	baseAttrs := []attribute.KeyValue{
		attribute.String("name", opName),
		attribute.String("type", opType.String()),
	}
	var err error
	var waitTime time.Duration
	var attempt int
	for {
		if (h.maxRetries > 0 && attempt >= h.maxRetries) || waitTime >= h.maxWaitTime {
			break
		}

		if err = operation(); err == nil {
			return nil
		}

		var apiErr *xrpc.Error
		var ok bool
		// short circuit if no atproto error
		if apiErr, ok = err.(*xrpc.Error); !ok {
			return nil
		}
		// suppress repo takedown / deactivated errors
		if suppressATProtoErr(err) {
			return nil
		}
		switch apiErr.StatusCode {
		case http.StatusTooManyRequests:
			// Record TooManyRequests metric
			h.metrics.rateLimit.Add(ctx, 1, metric.WithAttributes(baseAttrs...))
			waitTime = h.calculateWaitTime(apiErr, attempt, opType)
			h.metrics.currentBackoff.Add(ctx, waitTime.Seconds(), metric.WithAttributes(baseAttrs...))
			h.metrics.retryAttempts.Add(ctx, 1, metric.WithAttributes(baseAttrs...))
			h.log.With("action", "retry", "op-name", opName, "op-type", opType, "wait", waitTime, "attempt", attempt+1, "max-retry", h.maxRetries, "max-wait", h.maxWaitTime).Warn(fmt.Sprintf("Rate limit exceeded. Waiting %v", waitTime))
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled while waiting for rate limit: %w", ctx.Err())
			// wait alloted cooldown period
			case <-time.After(waitTime):
				attempt++
				continue
			}
		}
	}
	var retryErr error
	if h.maxRetries > 0 {
		retryErr = fmt.Errorf("%s op: %s failed after %d retries: %w", opType, opName, h.maxRetries, err)
		h.metrics.failures.Add(ctx, 1,
			metric.WithAttributes(baseAttrs...),
			metric.WithAttributes(
				attribute.String("failure_type",
					string(FailureMaxAttempts)),
			),
		)
	} else {
		retryErr = fmt.Errorf("%s op: %s failed after %v: %w", opType, opName, h.maxWaitTime, err)
		h.metrics.failures.Add(ctx, 1,
			metric.WithAttributes(baseAttrs...),
			metric.WithAttributes(
				attribute.String("failure_type",
					string(FailureMaxWaitTime)),
			),
		)
	}
	h.log.WithErrorMsg(retryErr, "Retry Exhausted", "action", "retry", "op", opType, "max-retry", h.maxRetries, "max-wait", h.maxWaitTime)
	return retryErr
}

// calculateWaitTime determines how long to wait before retrying upto maxWaitTime
func (h *RateLimitHandler) calculateWaitTime(apiErr *xrpc.Error, attempt int, opType OperationType) time.Duration {
	// use specified rate limit TTL if specififed
	if apiErr != nil && apiErr.Ratelimit != nil {
		return time.Since(apiErr.Ratelimit.Reset).Abs()
	}

	// otherwise fall back to exponential backoff based on read vs write op
	baseWait := h.readBaseWaitTime
	if opType == WriteOperation {
		baseWait = h.writeBaseWaitTime
	}

	// 2^n expoential backoff
	// 1st retry: 500ms
	// 2nd retry: 1s
	// 3rd retry: 2s
	// 4th retry: 4s
	backoff := baseWait * time.Duration(1<<uint(attempt))
	if backoff > h.maxWaitTime {
		backoff = h.maxWaitTime
	}
	return backoff
}

func suppressATProtoErr(err error) bool {
	// suppress the following errors:
	// 400: RepoDeactivated
	// 400: RepoTakendown
	switch err.Error() {
	case ErrRepoTakedown:
		return true
	case ErrRepoDeactivated:
		return true
	}

	var atErr *xrpc.Error
	// Unwrap error to check if it's an *xrpc.Error
	if unwrappedErr := errors.Unwrap(err); unwrappedErr != nil {
		if !errors.As(unwrappedErr, &atErr) {
			// short circuit if no atproto error
			return false
		}
		switch atErr.StatusCode {
		case http.StatusBadRequest:
			// suppress the following errors:
			// 400: RepoDeactivated
			// 400: RepoTakendown
			switch atErr.Error() {
			case ErrRepoTakedown:
				return true
			case ErrRepoDeactivated:
				return true
			}
		}
	}
	return false
}
