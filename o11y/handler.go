package o11y

import "log/slog"

// OTELErrorHandler implements the otel.ErrorHandler interface
type OTELErrorHandler struct {
	logger *slog.Logger
}

func NewOTELErrorHandler(logger *slog.Logger) *OTELErrorHandler {
	return &OTELErrorHandler{logger: logger}
}

func (h *OTELErrorHandler) Handle(err error) {
	h.logger.Error("OTEL export error",
		"error", err,
		"engine", "otel",
		"type", "metric_export_failed",
	)
}
