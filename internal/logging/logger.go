package logging

import (
	"fmt"
	"os"
	"strings"

	"log/slog"

	"github.com/l0p7/passctrl/internal/config"
)

// New shapes slog so emitted telemetry matches the runtime policy described in the design docs.
func New(cfg config.LoggingConfig) (*slog.Logger, error) {
	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info", "":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("logging: unsupported level %q", cfg.Level)
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json", "":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("logging: unsupported format %q", cfg.Format)
	}

	logger := slog.New(handler).With(slog.String("component", "passctrl"))
	if cfg.CorrelationHeader != "" {
		logger = logger.With(slog.String("correlation_header", cfg.CorrelationHeader))
	}
	return logger, nil
}
