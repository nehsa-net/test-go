// Command weatherd is the reference service under test.
//
// main() does three things and no more: read config, wire dependencies, serve.
// Everything worth testing lives in a package it can import, which is what
// makes the unit tier possible at all — you cannot call func main() from a test.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/nehsa-net/test-go/internal/config"
	"github.com/nehsa-net/test-go/internal/httpapi"
	"github.com/nehsa-net/test-go/internal/store"
	"github.com/nehsa-net/test-go/internal/weather"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("service exited", "error", err)
		os.Exit(1)
	}
}

// run returns an error instead of calling os.Exit, so an e2e test could import
// and drive it in-process if it ever wanted to.
func run(logger *slog.Logger) error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	httpClient := &http.Client{Timeout: time.Duration(cfg.RequestTimeout) * time.Second}
	client := weather.NewClient(cfg.UpstreamURL, cfg.APIKey, httpClient)

	opts := []weather.Option{}
	if cfg.DatabaseURL != "" {
		db, err := sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("opening database: %w", err)
		}
		defer func() {
			if cerr := db.Close(); cerr != nil {
				logger.Error("closing database", "error", cerr)
			}
		}()

		st := store.New(db)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := st.Migrate(ctx); err != nil {
			return fmt.Errorf("migrating: %w", err)
		}
		opts = append(opts, weather.WithRecorder(st))
		logger.Info("recording observations to postgres")
	}

	svc := weather.NewService(client, opts...)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(svc),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown matters to the e2e tier: the test sends SIGTERM and
	// expects the process to exit 0 rather than being killed.
	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("shutdown", "error", err)
		}
		close(idle)
	}()

	logger.Info("listening", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving: %w", err)
	}
	<-idle
	logger.Info("stopped cleanly")
	return nil
}
