package o11y

const (
	ENV                                     = "ENV"
	ENV_OTEL_EXPORTER_OTLP_ENDPOINT         = "OTEL_EXPORTER_OTLP_ENDPOINT"
	ENV_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	ENV_OTEL_EXPORTER_OTLP_TRACES_ENDPOINT  = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"

	DEFAULT_ENV                = "local"
	DEFAULT_OTEL_OTLP_ENDPOINT = "localhost:4317"
)
