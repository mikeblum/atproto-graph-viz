package bsky

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

type RateLimitFailureType string

const (
	FailureMaxAttempts  RateLimitFailureType = "max_attempts"
	FailureMaxWaitTime  RateLimitFailureType = "max_wait_time"
	FailureTimeout      RateLimitFailureType = "timeout"
	FailureNetworkError RateLimitFailureType = "network_error"
)

type RateLimitMetrics struct {
	retryAttempts  metric.Int64UpDownCounter
	waitDuration   metric.Float64Histogram
	rateLimit      metric.Int64Counter
	currentBackoff metric.Float64UpDownCounter
	failures       metric.Int64Counter
	statusCodes    metric.Int64Counter
}

func NewRateLimitMetrics(ctx context.Context) (*RateLimitMetrics, error) {
	meter := otel.GetMeterProvider().Meter(
		"atproto_rate_limiter",
		metric.WithInstrumentationVersion("0.1.0"),
	)

	retryAttempts, err := meter.Int64UpDownCounter(
		"atproto.rate_limit.retry_attempts",
		metric.WithDescription("Current number of retry attempts"),
		metric.WithUnit("{attempt}"),
	)
	if err != nil {
		return nil, err
	}

	waitDuration, err := meter.Float64Histogram(
		"atproto.rate_limit.wait_duration",
		metric.WithDescription("Time spent waiting due to rate limits"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	rateLimit, err := meter.Int64Counter(
		"atproto.rate_limit.hits",
		metric.WithDescription("Number of rate limit hits encountered"),
		metric.WithUnit("{hit}"),
	)
	if err != nil {
		return nil, err
	}

	currentBackoff, err := meter.Float64UpDownCounter(
		"atproto.rate_limit.current_backoff",
		metric.WithDescription("Current backoff duration for rate limiting"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	failures, err := meter.Int64Counter(
		"atproto.rate_limit.failures",
		metric.WithDescription("Rate limit failures by type"),
		metric.WithUnit("{failure}"),
	)
	if err != nil {
		return nil, err
	}

	statusCodes, err := meter.Int64Counter(
		"atproto.rate_limit.status_codes",
		metric.WithDescription("Rate limit HTTP status codes"),
		metric.WithUnit("{status_code}"),
	)
	if err != nil {
		return nil, err
	}

	return &RateLimitMetrics{
		retryAttempts:  retryAttempts,
		waitDuration:   waitDuration,
		rateLimit:      rateLimit,
		currentBackoff: currentBackoff,
		failures:       failures,
		statusCodes:    statusCodes,
	}, nil
}
