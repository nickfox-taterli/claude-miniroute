package config

import (
	"log"
	"os"
	"sync"
	"time"
)

// Reloader wraps a Config with hot-reload capability.
// It polls the config file for changes and atomically swaps the config.
type Reloader struct {
	mu      sync.RWMutex
	cfg     *Config
	path    string
	modTime time.Time
	done    chan struct{}
}

// NewReloader creates a Reloader that holds the initial config and watches path for changes.
func NewReloader(cfg *Config, path string) *Reloader {
	info, _ := os.Stat(path)
	var modTime time.Time
	if info != nil {
		modTime = info.ModTime()
	}
	return &Reloader{
		cfg:     cfg,
		path:    path,
		modTime: modTime,
		done:    make(chan struct{}),
	}
}

// Get returns the current config. Safe for concurrent use.
func (r *Reloader) Get() *Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

// StartWatching begins polling the config file every interval.
// Blocks until Stop is called. Typically run in a goroutine.
func (r *Reloader) StartWatching(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			r.checkAndReload()
		}
	}
}

// Stop terminates the watching goroutine.
func (r *Reloader) Stop() {
	close(r.done)
}

func (r *Reloader) checkAndReload() {
	info, err := os.Stat(r.path)
	if err != nil {
		log.Printf("config reload: stat error: %v", err)
		return
	}
	if info.ModTime().Equal(r.modTime) {
		return
	}

	newCfg, err := Load(r.path)
	if err != nil {
		log.Printf("config reload: validation failed, keeping current config: %v", err)
		return
	}

	r.mu.Lock()
	r.cfg = newCfg
	r.modTime = info.ModTime()
	r.mu.Unlock()

	log.Printf("config reload: successfully reloaded from %s", r.path)
}
