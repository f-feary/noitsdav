package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"noitsdav/internal/config"
	"noitsdav/internal/observability"
	"noitsdav/internal/server"
)

func main() {
	configPath := flag.String("config", "config.json", "path to JSON configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logger := observability.New(cfg.Logging.Level)
	app, err := server.NewApp(context.Background(), cfg, logger)
	if err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           app.Handler,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("server listening", "addr", cfg.ListenAddress)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown failed", "error", err)
	}
}
