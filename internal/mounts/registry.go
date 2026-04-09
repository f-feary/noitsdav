package mounts

import (
	"sort"
	"sync"
	"time"

	"noitsdav/internal/config"
)

type Status string

const (
	StatusAvailable   Status = "available"
	StatusUnavailable Status = "unavailable"
)

type Health struct {
	MountName     string
	Status        Status
	LastCheckedAt time.Time
	LastError     string
}

type Registry struct {
	mu      sync.RWMutex
	order   []string
	mounts  map[string]config.MountConfig
	healths map[string]Health
}

func NewRegistry(mounts []config.MountConfig) *Registry {
	r := &Registry{
		order:   make([]string, 0, len(mounts)),
		mounts:  make(map[string]config.MountConfig, len(mounts)),
		healths: make(map[string]Health, len(mounts)),
	}
	for _, mount := range mounts {
		r.order = append(r.order, mount.Name)
		r.mounts[mount.Name] = mount
		r.healths[mount.Name] = Health{MountName: mount.Name, Status: StatusUnavailable}
	}
	return r
}

func (r *Registry) List() []config.MountConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]config.MountConfig, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.mounts[name])
	}
	return out
}

func (r *Registry) Get(name string) (config.MountConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.mounts[name]
	return m, ok
}

func (r *Registry) SetHealth(name string, status Status, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.healths[name]
	h.MountName = name
	h.Status = status
	h.LastCheckedAt = time.Now()
	if err != nil {
		h.LastError = err.Error()
	} else {
		h.LastError = ""
	}
	r.healths[name] = h
}

func (r *Registry) Health(name string) (Health, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.healths[name]
	return h, ok
}

func (r *Registry) HealthyCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, h := range r.healths {
		if h.Status == StatusAvailable {
			count++
		}
	}
	return count
}

func (r *Registry) OrderedNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}

