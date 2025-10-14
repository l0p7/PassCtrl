package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/l0p7/passctrl/internal/config"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestNewRequiresHandler(t *testing.T) {
	cfg := config.DefaultConfig()
	if _, err := New(cfg, newTestLogger(), nil); err == nil {
		t.Fatalf("expected error when handler is nil")
	}
}

func TestNewUsesConfiguredAddress(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Listen.Address = "127.0.0.1"
	cfg.Server.Listen.Port = 9090

	srv, err := New(cfg, newTestLogger(), http.NewServeMux())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedAddr := "127.0.0.1:9090"
	if srv.httpServer.Addr != expectedAddr {
		t.Fatalf("expected addr %s, got %s", expectedAddr, srv.httpServer.Addr)
	}
}

func TestRunShutsDownWhenContextCancelled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Listen.Address = "127.0.0.1"
	cfg.Server.Listen.Port = 0

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv, err := New(cfg, newTestLogger(), handler)
	if err != nil {
		t.Fatalf("unexpected error building server: %v", err)
	}

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
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not return after cancellation")
	}
}
