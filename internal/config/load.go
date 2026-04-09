package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
)

var mountNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func Load(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.ListenAddress == "" {
		return errors.New("listen_address is required")
	}
	if c.Auth.Username == "" || c.Auth.Password == "" {
		return errors.New("auth.username and auth.password are required")
	}
	if c.Auth.Realm == "" {
		c.Auth.Realm = "noitsdav"
	}
	if len(c.Mounts) == 0 {
		return errors.New("at least one mount is required")
	}

	seen := map[string]struct{}{}
	for i := range c.Mounts {
		m := &c.Mounts[i]
		if m.Name == "" || m.Host == "" || m.Username == "" {
			return fmt.Errorf("mount %d missing required fields", i)
		}
		if !mountNamePattern.MatchString(m.Name) {
			return fmt.Errorf("mount %q has invalid name", m.Name)
		}
		if _, ok := seen[m.Name]; ok {
			return fmt.Errorf("duplicate mount name %q", m.Name)
		}
		seen[m.Name] = struct{}{}
		if m.Port == 0 {
			m.Port = 21
		}
		if m.RootPath == "" {
			m.RootPath = "/"
		}
		m.RootPath = path.Clean("/" + m.RootPath)
	}

	return nil
}

