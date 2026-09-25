package priv

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// task 101: the helper's level follows occulited's through one operation that carries a level's
// name and nothing else; the per-request debug line names the operation and its program or path,
// never what it writes.
func TestLogLevelOperation(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var levels, debug []string
	srv := &Server{
		Policy:      DefaultPolicy(t.TempDir(), "/usr/local/etc/occulite"),
		SetLogLevel: func(n string) { mu.Lock(); levels = append(levels, n); mu.Unlock() },
		Debug:       func(f string, a ...any) { mu.Lock(); debug = append(debug, fmt.Sprintf(f, a...)); mu.Unlock() },
	}
	go func() { _ = srv.Serve(ctx, l) }()
	c := Client{Socket: sock}

	for _, n := range []string{"debug", "warn", "info", "error"} {
		if err := c.SetLogLevel(ctx, n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	for _, bad := range []string{"trace", "", "INFO", "debug; reboot"} {
		if err := c.SetLogLevel(ctx, bad); err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
	if _, err := c.call(ctx, request{Op: opLogLevel, Name: "debug", Path: "/etc/shadow"}); err == nil {
		t.Error("a level with a path accepted")
	}
	if _, err := c.call(ctx, request{Op: opLogLevel, Name: "debug", Args: []string{"x"}}); err == nil {
		t.Error("a level with arguments accepted")
	}
	// a write's data never reaches the debug line
	_, _ = c.call(ctx, request{Op: "write", Path: "/nowhere/secret-file", Data: []byte("s3cr3t-token")})
	_, _ = c.call(ctx, request{Op: "run", Name: "hostname", Args: []string{"--password=hunter2"}})

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(levels, " ") != "debug warn info error" {
		t.Errorf("levels applied: %v", levels)
	}
	joined := strings.Join(debug, "\n")
	if !strings.Contains(joined, "helper: loglevel debug") || !strings.Contains(joined, "helper: write /nowhere/secret-file") || !strings.Contains(joined, "helper: run hostname") {
		t.Errorf("debug lines:\n%s", joined)
	}
	if strings.Contains(joined, "s3cr3t") || strings.Contains(joined, "hunter2") {
		t.Errorf("a secret in the debug lines:\n%s", joined)
	}
}

// A helper without the hook refuses the operation instead of pretending.
func TestLogLevelOperationWithoutHook(t *testing.T) {
	srv := &Server{Policy: DefaultPolicy(t.TempDir(), "/usr/local/etc/occulite")}
	if res := srv.do(context.Background(), request{Op: opLogLevel, Name: "debug"}); res.OK || !strings.Contains(res.Error, "refused") {
		t.Errorf("%+v", res)
	}
}
