// Command quince is the core daemon. qn.0 ships two subcommands:
//
//	quince serve     # serve the embedded UI + GET /api/health
//	quince version   # print the build version
//
// Bootstrap config is env-only (contracts.md §6); everything else will move to
// /data/config.yml in qn.1. Later rungs grow `serve` into the full device/job engine.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/novkostya/quince/core/internal/httpapi"
	"github.com/novkostya/quince/core/internal/version"
	"github.com/novkostya/quince/core/internal/webui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "quince: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("a subcommand is required")
	}
	switch args[0] {
	case "serve":
		return serve()
	case "version":
		fmt.Println(version.String())
		return nil
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "quince %s\n\nUsage:\n  quince serve     serve the UI + API\n  quince version   print version\n", version.String())
}

func serve() error {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	listen := envOr("QUINCE_LISTEN", ":8080")
	srv := &http.Server{
		Addr:              listen,
		Handler:           httpapi.NewRouter(log),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("quince serving",
			"version", version.String(),
			"listen", listen,
			"ui_embedded", webui.Built(),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutdown signal received, draining")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
