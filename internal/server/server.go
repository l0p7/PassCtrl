package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/l0p7/passctrl/internal/config"
)

// Server owns the HTTP lifecycle and orchestrates graceful shutdown.
type Server struct {
	cfg        config.Config
	logger     *slog.Logger
	httpServer *http.Server
	once       sync.Once
}

// New equips the lifecycle agent with the first handler hook so later reloads inherit consistent listener settings.
func New(cfg config.Config, logger *slog.Logger, handler http.Handler) (*Server, error) {
	if handler == nil {
		return nil, errors.New("server: handler required")
	}

	addr := net.JoinHostPort(cfg.Server.Listen.Address, strconv.Itoa(cfg.Server.Listen.Port))
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return &Server{
		cfg:        cfg,
		logger:     logger.With(slog.String("agent", "lifecycle")),
		httpServer: httpSrv,
	}, nil
}

// Run keeps the lifecycle agent active until shutdown signals arrive, ensuring graceful exits over abrupt restarts.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.logger.Info("http listener starting", slog.String("address", s.httpServer.Addr))
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("server: listen: %w", err)
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.shutdown(shutdownCtx); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}
}

// shutdown collapses the listener once to stop duplicate shutdown work during cascading cancellations.
func (s *Server) shutdown(ctx context.Context) error {
	var shutdownErr error
	s.once.Do(func() {
		s.logger.Info("http listener shutting down")
		shutdownErr = s.httpServer.Shutdown(ctx)
	})
	return shutdownErr
}
