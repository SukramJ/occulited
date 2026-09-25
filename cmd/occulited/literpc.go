package main

import (
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/config"
)

// traceStore keeps the RPC trace's switch (task 79, rpctrace.Store) in occulited.json's rpc
// block: read at start, written on every change.
type traceStore struct {
	mu   sync.Mutex
	path string
}

func (t *traceStore) ReadTrace() (string, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cfg, err := config.Load(t.path)
	if err != nil {
		return "", time.Time{}
	}
	until, _ := time.Parse(time.RFC3339, cfg.RPC.Trace.Until)
	return cfg.RPC.Trace.Mode, until
}

func (t *traceStore) WriteTrace(mode string, until time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	cfg, err := config.Load(t.path)
	if err != nil {
		return err
	}
	cfg.RPC.Trace = config.TraceConfig{}
	if mode != "" && mode != "off" {
		cfg.RPC.Trace.Mode = mode
	}
	if !until.IsZero() {
		cfg.RPC.Trace.Until = until.UTC().Format(time.RFC3339)
	}
	return config.Save(t.path, cfg)
}
