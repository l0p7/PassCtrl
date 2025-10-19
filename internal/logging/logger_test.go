package logging

import (
	"testing"

	"github.com/l0p7/passctrl/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewAcceptsKnownLevelsAndFormats(t *testing.T) {
	logger, err := New(config.LoggingConfig{Level: "info", Format: "json", CorrelationHeader: "X-Request-ID"})
	require.NoError(t, err)
	require.NotNil(t, logger)
}

func TestNewRejectsUnknownLevel(t *testing.T) {
	_, err := New(config.LoggingConfig{Level: "verbose"})
	require.Error(t, err)
}

func TestNewRejectsUnknownFormat(t *testing.T) {
	_, err := New(config.LoggingConfig{Format: "binary"})
	require.Error(t, err)
}
