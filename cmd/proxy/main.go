package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/opsorigin/core/pkg/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	proxy := NewProxy(cfg)
	go func() {
		if err := proxy.Start(ctx); err != nil {
			slog.Error("proxy stopped", "error", err)
			cancel()
		}
	}()

	slog.Info("proxy agent started", "core_addr", cfg.Proxy.CoreAddr)
	<-sigCh
	slog.Info("shutting down proxy...")
	proxy.Stop()
}
