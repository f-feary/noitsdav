package unit

import (
	"os"
	"path/filepath"
	"testing"

	"noitsdav/internal/config"
)

func TestLoadConfigDefaultsAndValidation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "config.json")
	body := `{
	  "listen_address": ":8080",
	  "auth": {"username": "dav", "password": "pass"},
	  "mounts": [{"name": "media", "host": "127.0.0.1", "username": "ftp", "password": "ftp", "root_path": "/"}]
	}`
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(file)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if cfg.Auth.Realm != "noitsdav" {
		t.Fatalf("expected default realm, got %q", cfg.Auth.Realm)
	}
	if cfg.Mounts[0].Port != 21 {
		t.Fatalf("expected default port 21, got %d", cfg.Mounts[0].Port)
	}
}

func TestValidateRejectsDuplicateMounts(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		ListenAddress: ":8080",
		Auth:          config.AuthConfig{Username: "dav", Password: "pass"},
		Mounts: []config.MountConfig{
			{Name: "dup", Host: "a", Username: "u", Password: "p", RootPath: "/"},
			{Name: "dup", Host: "b", Username: "u", Password: "p", RootPath: "/"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected duplicate mount validation error")
	}
}

