package bsky

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/xrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	opName = "test"
)

func TestRateLimit(t *testing.T) {
	t.Run("retry once deadline", retryOnceDeadlineTest)
	t.Run("retry once backoff", retryOnceBackoffTest)
	t.Run("max retries set to MAX_RETRIES by default", maxRetriesTest)
	t.Run("max retries exceeded", maxRetriesExceededTest)
	t.Run("retry context cancelled", retryContextCancelledTest)
	t.Run("retry after specified deadline", resetDeadlineTest)
	t.Run("op=read retry upto MAX_WAIT if MAX_RETRIES = 0 without deadline", func(t *testing.T) {
		exponentialBackoffTest(t, ReadOperation)
	})
	t.Run("op=write retry upto MAX_WAIT if MAX_RETRIES = 0 without deadline", func(t *testing.T) {
		exponentialBackoffTest(t, WriteOperation)
	})
	t.Run("op=read retry upto MAX_RETRIES without deadline", func(t *testing.T) {
		exponentialBackoffWithRetryMaxTest(t, ReadOperation)
	})
	t.Run("op=write retry upto MAX_RETRIES without deadline", func(t *testing.T) {
		exponentialBackoffWithRetryMaxTest(t, WriteOperation)
	})
}

func retryOnceDeadlineTest(t *testing.T) {
	handler := NewRateLimitHandler(&xrpc.Client{})
	var attempt int
	reset := time.Now().UTC()
	err := handler.withRetry(context.Background(), ReadOperation, opName, func() error {
		attempt++
		reset = reset.Add(time.Duration(attempt) * time.Millisecond)
		if attempt <= 1 {
			return &xrpc.Error{
				StatusCode: http.StatusTooManyRequests,
				Ratelimit: &xrpc.RatelimitInfo{
					// ratelimit-reset deadline
					Reset: reset,
				},
			}
		}
		return nil
	})
	assert.Nil(t, err)
}

func retryOnceBackoffTest(t *testing.T) {
	handler := NewRateLimitHandler(&xrpc.Client{})
	var attempt int
	reset := time.Now().UTC()
	err := handler.withRetry(context.Background(), ReadOperation, opName, func() error {
		attempt++
		reset = reset.Add(time.Duration(attempt) * time.Millisecond)
		if attempt <= 1 {
			return &xrpc.Error{
				StatusCode: http.StatusTooManyRequests,
			}
		}
		return nil
	})
	assert.Nil(t, err)
}

func maxRetriesTest(t *testing.T) {
	handler := NewRateLimitHandler(&xrpc.Client{})
	conf := NewConf()
	assert.Equal(t, conf.MaxRetries(), handler.maxRetries)
	assert.Equal(t, DEFAULT_MAX_RETRIES, handler.maxRetries)
}

func maxRetriesExceededTest(t *testing.T) {
	handler := NewRateLimitHandler(&xrpc.Client{})
	handler.maxRetries = 2
	reset := time.Now().UTC()
	attempt := 0
	err := handler.withRetry(context.Background(), ReadOperation, opName, func() error {
		attempt++
		reset = reset.Add(time.Duration(attempt) * time.Millisecond)
		return &xrpc.Error{
			StatusCode: http.StatusTooManyRequests,
			Ratelimit: &xrpc.RatelimitInfo{
				// ratelimit-reset deadline
				Reset: reset,
			},
		}
	})

	require.Error(t, err)
	assert.Equal(t, 2, attempt)
	assert.Contains(t, err.Error(), "operation failed after 2 retries")
}

func retryContextCancelledTest(t *testing.T) {
	handler := NewRateLimitHandler(&xrpc.Client{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	attempt := 0
	err := handler.withRetry(ctx, ReadOperation, opName, func() error {
		attempt++
		return &xrpc.Error{
			StatusCode: http.StatusTooManyRequests,
			Ratelimit: &xrpc.RatelimitInfo{
				// ratelimit-reset deadline
				Reset: time.Now().UTC().Add(time.Second),
			},
		}
	})

	require.Error(t, err)
	assert.Equal(t, 1, attempt)
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
}

func resetDeadlineTest(t *testing.T) {
	handler := NewRateLimitHandler(&xrpc.Client{})
	reset := time.Now().UTC()
	attempt := 0
	err := handler.withRetry(context.Background(), ReadOperation, opName, func() error {
		attempt++
		reset = reset.Add(time.Duration(attempt) * time.Millisecond)
		return &xrpc.Error{
			StatusCode: http.StatusTooManyRequests,
			Ratelimit: &xrpc.RatelimitInfo{
				// ratelimit-reset deadline
				Reset: reset,
			},
		}
	})

	require.Error(t, err)
	assert.Equal(t, 3, attempt)
	assert.Contains(t, err.Error(), "operation failed after 3 retries")
}

func exponentialBackoffTest(t *testing.T, op OperationType) {
	handler := NewRateLimitHandler(&xrpc.Client{})
	// disable max retries
	handler.maxRetries = 0
	handler.maxWaitTime = 5 * time.Second
	handler.readBaseWaitTime = time.Millisecond
	handler.writeBaseWaitTime = 2 * time.Millisecond
	start := time.Now().UTC()
	err := handler.withRetry(context.Background(), op, opName, func() error {
		return &xrpc.Error{
			StatusCode: http.StatusTooManyRequests,
		}
	})
	duration := time.Since(start)
	assert.Contains(t, err.Error(), "operation failed after 5s")
	assert.LessOrEqual(t, handler.maxWaitTime, duration)
}

func exponentialBackoffWithRetryMaxTest(t *testing.T, op OperationType) {
	handler := NewRateLimitHandler(&xrpc.Client{})
	// disable max retries
	handler.maxRetries = 5
	handler.maxWaitTime = time.Second
	handler.readBaseWaitTime = time.Millisecond
	handler.writeBaseWaitTime = 2 * time.Millisecond
	start := time.Now().UTC()
	err := handler.withRetry(context.Background(), op, opName, func() error {
		return &xrpc.Error{
			StatusCode: http.StatusTooManyRequests,
		}
	})
	duration := time.Since(start)
	assert.Contains(t, err.Error(), "operation failed after 5 retries")
	assert.GreaterOrEqual(t, handler.maxWaitTime, duration)
}
