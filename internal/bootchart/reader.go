package bootchart

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrUnsupported: no systemd, no timeline.
var ErrUnsupported = errors.New("the boot timeline needs systemd")

// Reader reads the running boot's timeline and keeps it: once the boot has finished its stamps do
// not change (a unit started later is not part of it), and the call over every unit takes 3.5 s on
// the Charly (a Pi 3; 0.7 s on the x86_64 VM, 1.8 s on the Pi 4, measured 2026-09-12). A boot still
// starting is read again after a few seconds.
type Reader struct {
	// Root is the filesystem root /proc is read below: "/" on a box.
	Root string
	// Systemctl runs systemctl with these arguments; nil = no systemd.
	Systemctl func(ctx context.Context, args ...string) ([]byte, error)
	// Now is the wall clock; nil = time.Now.
	Now func() time.Time

	mu     sync.Mutex
	cached *Timeline
	at     time.Time

	liveMu sync.Mutex
	live   map[string]Live
	liveAt time.Time
}

// liveTTL is how long the units' live state is kept (B-153): the call over every unit takes about
// 4 s on the Charly, so a page opened twice in a row does not pay it twice.
const liveTTL = 30 * time.Second

// unfinishedTTL is how long the timeline of a boot still starting is kept.
const unfinishedTTL = 5 * time.Second

func (r *Reader) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reader) path(p string) string {
	root := r.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, p)
}

// BootID is this boot's id as journalctl prints it: /proc's UUID without its dashes.
func (r *Reader) BootID() string {
	b, _ := os.ReadFile(r.path("/proc/sys/kernel/random/boot_id"))
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(string(b))), "-", "")
}

func (r *Reader) uptime() (time.Duration, bool) {
	b, err := os.ReadFile(r.path("/proc/uptime"))
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil || s <= 0 {
		return 0, false
	}
	return time.Duration(s * float64(time.Second)), true
}

// Current is the running boot's timeline.
func (r *Reader) Current(ctx context.Context) (*Timeline, error) {
	if r.Systemctl == nil {
		return nil, ErrUnsupported
	}
	id := r.BootID()
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.cached; c != nil && c.BootID == id && (c.Finished || r.now().Sub(r.at) < unfinishedTTL) {
		return c, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	manager, err := r.Systemctl(cctx, ManagerArgs()...)
	if err != nil {
		return nil, fmt.Errorf("systemctl show: %w", err)
	}
	units, err := r.Systemctl(cctx, UnitsArgs()...)
	if err != nil {
		return nil, fmt.Errorf("systemctl show: %w", err)
	}
	var nowUS int64
	up, ok := r.uptime()
	if ok {
		nowUS = up.Microseconds()
	}
	t := Build(manager, units, nowUS)
	t.BootID = id
	if ok {
		t.Started = r.now().Add(-up).Format(time.RFC3339)
	}
	r.cached, r.at = &t, r.now()
	return &t, nil
}

// Live is every unit's state now and when its last start began (B-153), read at most every liveTTL.
func (r *Reader) Live(ctx context.Context) (map[string]Live, error) {
	if r.Systemctl == nil {
		return nil, ErrUnsupported
	}
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	if r.live != nil && r.now().Sub(r.liveAt) < liveTTL {
		return r.live, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := r.Systemctl(cctx, LiveArgs()...)
	if err != nil {
		return nil, fmt.Errorf("systemctl show: %w", err)
	}
	r.live, r.liveAt = ParseLive(out), r.now()
	return r.live, nil
}
