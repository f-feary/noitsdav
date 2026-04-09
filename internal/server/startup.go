package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"noitsdav/internal/auth"
	"noitsdav/internal/config"
	"noitsdav/internal/ftpfs"
	"noitsdav/internal/mounts"
)

type App struct {
	Config   *config.Config
	Registry *mounts.Registry
	Clients  map[string]*ftpfs.Client
	Handler  http.Handler
}

func NewApp(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*App, error) {
	registry := mounts.NewRegistry(cfg.Mounts)
	clients := make(map[string]*ftpfs.Client, len(cfg.Mounts))
	for _, mount := range cfg.Mounts {
		client := ftpfs.NewClient(mount, logger)
		clients[mount.Name] = client
		err := client.Probe(ctx)
		if err != nil {
			registry.SetHealth(mount.Name, mounts.StatusUnavailable, err)
			logger.Warn("mount unavailable at startup", "mount", mount.Name, "error", err)
			continue
		}
		registry.SetHealth(mount.Name, mounts.StatusAvailable, nil)
		logger.Info("mount available at startup", "mount", mount.Name)
	}
	if registry.HealthyCount() == 0 {
		return nil, errors.New("no healthy mounts available at startup")
	}

	base := NewHandler(registry, clients, logger)
	handler := auth.Middleware(cfg.Auth.Username, cfg.Auth.Password, cfg.Auth.Realm, base)
	return &App{
		Config:   cfg,
		Registry: registry,
		Clients:  clients,
		Handler:  handler,
	}, nil
}
