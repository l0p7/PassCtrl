package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/l0p7/passctrl/internal/config"
	"github.com/stretchr/testify/require"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestNewRequiresHandler(t *testing.T) {
	cfg := config.DefaultConfig()
	_, err := New(cfg, newTestLogger(), nil)
	require.Error(t, err)
}

func TestNewUsesConfiguredAddress(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Listen.Address = "127.0.0.1"
	cfg.Server.Listen.Port = 9090

	srv, err := New(cfg, newTestLogger(), http.NewServeMux())
	require.NoError(t, err)
	expectedAddr := "127.0.0.1:9090"
	require.Equal(t, expectedAddr, srv.httpServer.Addr)
}

func TestRunShutsDownWhenContextCancelled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Listen.Address = "127.0.0.1"
	cfg.Server.Listen.Port = 0

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv, err := New(cfg, newTestLogger(), handler)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "server did not return after cancellation")
	}
}
