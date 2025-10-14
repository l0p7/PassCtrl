package logging

import (
	"testing"

	"github.com/l0p7/passctrl/internal/config"
)

func TestNewAcceptsKnownLevelsAndFormats(t *testing.T) {
	logger, err := New(config.LoggingConfig{Level: "info", Format: "json", CorrelationHeader: "X-Request-ID"})
	if err != nil {
		t.Fatalf("expected logger construction to succeed: %v", err)
	}
	if logger == nil {
		t.Fatalf("logger should not be nil")
	}
}

func TestNewRejectsUnknownLevel(t *testing.T) {
	if _, err := New(config.LoggingConfig{Level: "verbose"}); err == nil {
		t.Fatalf("expected error for unsupported level")
	}
}

func TestNewRejectsUnknownFormat(t *testing.T) {
	if _, err := New(config.LoggingConfig{Format: "binary"}); err == nil {
		t.Fatalf("expected error for unsupported format")
	}
}
