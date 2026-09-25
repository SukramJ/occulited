package radio

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// HBRFETHWatch retries a configured HB-RF-ETH that is not connected (task 218, the maintainer's
// decision of 2026-09-24): unreachable at boot, powered off since beyond the kernel's own
// reconnect, or set on the page a moment ago. Every Every (30 s; every 2 minutes after ten tries)
// it starts the radio hotplug - `occulited radio hotplug`, as root in its unit - whose rescan
// connects the board and re-plans the radio stack when the module appears (hotplug.go). A board
// that is connected, or no board configured, costs a file read.
//
// openccu-lite B-218: a board that was connected and drops is the kernel's to reconnect (its
// autoreconnect tries every second). A hotplug started in the middle of that re-wrote the
// module's address, found the node with connection_state 0, and re-planned without the module -
// rfd and multimacd stopped, BidCos-RF out of InterfacesList.xml - for a 10 s drop the kernel
// would have mended alone (measured on the lab system: a 12 s drop between two ticks, rfd kept its
// PID and its CONNECTED). So a board that connected once at this address is the kernel's while its
// module is loaded (task 218: "a board that drops later is the kernel's autoreconnect"): the
// warning stays, no hotplug starts. When the kernel has the board back, Healthy asks the daemons on
// it after Verify whether they have their module: after a power loss of the board the module was
// reset, which the daemons may not take without a restart; a No restarts them (Restart).
//
// But the kernel does not always get it back: after a real power cycle of the board (the lab,
// 2026-09-25 14:43) its autoreconnect timed out every 0.6 s for eleven minutes while the board
// answered ping and HTTP with no host connected - the upstream gap task 218 names. A fresh connect
// (the address written again) takes it at once. So a loss longer than Grace - a link drop came
// back within 5 s, the board boots in about 11 s - is the hotplug's after all: the watch leaves
// HBRFETHReconnectFile for the rescan, which then connects afresh instead of waiting for the kernel.
type HBRFETHWatch struct {
	Root  string
	Every time.Duration
	// Start starts the hotplug unit (systemctl start --no-block occu-radio-hotplug.service)
	Start func(ctx context.Context) error
	Log   *slog.Logger
	// Verify is how long after the kernel reconnected the board the daemons are asked; 0 = 30 s.
	Verify time.Duration
	// Grace is how long a lost board is left to the kernel's reconnect before the hotplug connects
	// it afresh; 0 = 45 s.
	Grace time.Duration
	// Healthy says whether the daemons on the board have its module (and why not); nil = no check.
	// An error is no answer: asked again at the next tick, three times, then taken as a No.
	Healthy func(ctx context.Context) (ok bool, why string, err error)
	// Restart restarts the daemons on the board; nil = the hotplug is started instead.
	Restart func(ctx context.Context) error
	// Now is the clock; nil = time.Now.
	Now func() time.Time

	mu      sync.Mutex
	tries   int
	lastTry time.Time
	lastErr string
	kick    chan struct{}
	// connAddr is the address the board was last seen connected at; lostAt when it went since
	// (the kernel is reconnecting); backAt when the kernel had it back after a loss, until the
	// daemons are asked; checkErrs the unanswered asks
	connAddr  string
	lostAt    time.Time
	backAt    time.Time
	checkErrs int
	restarts  int
}

// HBRFETHStatus is the watch's state for the API.
type HBRFETHStatus struct {
	Address      string     `json:"address"`
	Connected    bool       `json:"connected"`
	ModuleLoaded bool       `json:"module_loaded"`
	Retrying     bool       `json:"retrying"`
	Tries        int        `json:"tries"`
	LastTry      *time.Time `json:"last_try,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	// Reconnecting: the board was connected and its link is lost; the kernel reconnects it (B-218)
	Reconnecting bool       `json:"reconnecting,omitempty"`
	LostSince    *time.Time `json:"lost_since,omitempty"`
	// Restarts is how often the daemons on the board were restarted after a reconnect (B-218).
	Restarts int `json:"restarts,omitempty"`
}

func (w *HBRFETHWatch) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *HBRFETHWatch) verify() time.Duration {
	if w.Verify > 0 {
		return w.Verify
	}
	return 30 * time.Second
}

func (w *HBRFETHWatch) grace() time.Duration {
	if w.Grace > 0 {
		return w.Grace
	}
	return 45 * time.Second
}

// HBRFETHReconnectFile tells the rescan to connect the board afresh (the address written again)
// rather than leave it to the kernel's reconnect: the watch writes the address into it when a loss
// outlasted its grace; the rescan removes it. In occulited's state directory, which the daemon may
// write and the hotplug (root) reads.
const HBRFETHReconnectFile = "/usr/local/etc/occulite/hbrfeth-reconnect"

func (w *HBRFETHWatch) every() time.Duration {
	if w.Every > 0 {
		return w.Every
	}
	return 30 * time.Second
}

// Kick asks for a try now (a changed address).
func (w *HBRFETHWatch) Kick() {
	w.mu.Lock()
	k := w.kick
	w.tries = 0
	w.connAddr, w.lostAt, w.backAt = "", time.Time{}, time.Time{} // a new address: its first connect
	w.mu.Unlock()
	if k != nil {
		select {
		case k <- struct{}{}:
		default:
		}
	}
}

// Status is the configured address, the module's state and the retries.
func (w *HBRFETHWatch) Status() HBRFETHStatus {
	addr := HBRFETHAddress(w.Root)
	c, loaded := HBRFETHConnected(w.Root)
	w.mu.Lock()
	defer w.mu.Unlock()
	st := HBRFETHStatus{Address: addr, Connected: c && addr != "", ModuleLoaded: loaded, Tries: w.tries, LastError: w.lastErr, Restarts: w.restarts}
	st.Retrying = addr != "" && !st.Connected
	if !w.lastTry.IsZero() {
		t := w.lastTry
		st.LastTry = &t
	}
	if st.Retrying && !w.lostAt.IsZero() && w.connAddr == addr {
		st.Reconnecting = true
		t := w.lostAt
		st.LostSince = &t
	}
	return st
}

// step is one tick: a hotplug where one is needed - a board configured and never connected here
// (at boot, or set a moment ago), its kernel module gone, or a board connected but no longer
// configured (the page removed it: the hotplug lets it go) - and the daemons asked after the
// kernel had the board back.
func (w *HBRFETHWatch) step(ctx context.Context, log *slog.Logger) {
	addr := HBRFETHAddress(w.Root)
	c, loaded := HBRFETHConnected(w.Root)
	now := w.now()
	w.mu.Lock()
	var hotplug, ask bool
	switch {
	case addr == "":
		w.connAddr, w.lostAt, w.backAt = "", time.Time{}, time.Time{}
		hotplug = loaded && c
	case c:
		// back after a loss the kernel mended, or connected by a hotplug of the watch's (also one
		// after a restart of occulited during a loss, B-218's power cycle): the daemons are asked
		if (w.connAddr == addr && !w.lostAt.IsZero()) || w.tries > 0 {
			log.Info("radio: the kernel has the HB-RF-ETH back; the daemons on it are asked shortly", "address", addr, "lost_for", now.Sub(w.lostAt).Round(time.Second))
			w.backAt, w.checkErrs = now, 0
		}
		w.connAddr, w.lostAt, w.tries = addr, time.Time{}, 0
		ask = !w.backAt.IsZero() && now.Sub(w.backAt) >= w.verify()
	default: // configured, not connected
		w.backAt = time.Time{}
		if w.connAddr == addr && loaded {
			if w.lostAt.IsZero() {
				w.lostAt = now
				log.Info("radio: the HB-RF-ETH's link is lost; the kernel reconnects it", "address", addr)
			}
			if now.Sub(w.lostAt) >= w.grace() {
				// the kernel did not get it back (a power cycle of the board): a fresh connect
				hotplug = true
				if w.tries == 0 {
					log.Warn("radio: the kernel did not get the HB-RF-ETH back - the radio hotplug connects it afresh", "address", addr, "lost_for", now.Sub(w.lostAt).Round(time.Second))
				}
			}
		} else {
			hotplug = true
		}
	}
	w.mu.Unlock()
	if ask {
		w.check(ctx, log, addr)
	}
	if !hotplug || w.Start == nil {
		if !hotplug {
			w.mu.Lock()
			if !(addr != "" && !c) {
				w.tries = 0
			}
			w.mu.Unlock()
		}
		return
	}
	if addr != "" {
		// every hotplug the watch starts connects the board afresh - also one after a restart of
		// occulited during a loss, which the watch cannot tell from a board never connected; only a
		// hotplug of udev's leaves a reconnecting board to the kernel
		if err := os.WriteFile(Detector{Root: w.Root}.path(HBRFETHReconnectFile), []byte(addr+"\n"), 0o644); err != nil {
			log.Warn("radio: the note for the hotplug could not be written", "err", err)
		}
	}
	err := w.Start(ctx)
	w.mu.Lock()
	w.tries++
	w.lastTry = now
	w.lastErr = ""
	if err != nil {
		w.lastErr = err.Error()
	}
	n := w.tries
	w.mu.Unlock()
	if n == 1 || err != nil {
		log.Info("radio: HB-RF-ETH not connected - the radio hotplug tries it", "address", addr, "err", err)
	}
}

// check asks the daemons on the board, once the kernel has had it back for Verify.
func (w *HBRFETHWatch) check(ctx context.Context, log *slog.Logger, addr string) {
	ok, why, err := true, "", error(nil)
	if w.Healthy != nil {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		ok, why, err = w.Healthy(cctx)
		cancel()
	}
	w.mu.Lock()
	if err != nil {
		w.checkErrs++
		if w.checkErrs < 3 {
			w.mu.Unlock()
			log.Info("radio: the daemons on the HB-RF-ETH did not answer; asked again at the next tick", "address", addr, "err", err)
			return
		}
		ok, why = false, "no answer: "+err.Error()
	}
	w.backAt, w.checkErrs = time.Time{}, 0
	if ok {
		w.mu.Unlock()
		log.Info("radio: the daemons on the HB-RF-ETH have their module again - no restart", "address", addr)
		return
	}
	w.restarts++
	w.mu.Unlock()
	log.Warn("radio: the daemons on the HB-RF-ETH do not have its module after the reconnect (a reset of the board?) - restarting them", "address", addr, "why", why)
	restart := w.Restart
	if restart == nil {
		restart = w.Start
	}
	if restart == nil {
		return
	}
	if err := restart(ctx); err != nil {
		log.Warn("radio: restarting the daemons on the HB-RF-ETH failed", "address", addr, "err", err)
	}
}

// Run tries until ctx ends.
func (w *HBRFETHWatch) Run(ctx context.Context) {
	w.mu.Lock()
	w.kick = make(chan struct{}, 1)
	k := w.kick
	// a board the boot's detection connected is one the kernel reconnects from the start
	if addr := HBRFETHAddress(w.Root); addr != "" {
		if c, _ := HBRFETHConnected(w.Root); c {
			w.connAddr = addr
		}
	}
	w.mu.Unlock()
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTimer(w.every())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-k:
		}
		w.step(ctx, log)
		w.mu.Lock()
		next := w.every()
		if w.tries >= 10 {
			next = 4 * w.every()
		}
		w.mu.Unlock()
		t.Reset(next)
	}
}

// OnHBRFETH says which modules of the plan sit on an HB-RF-ETH (openccu-lite B-218): the BidCos-RF
// role, the HmIP role, and the daemons that open the board's node, in start order.
func (p Plan) OnHBRFETH() (hmrf, hmip *Role, daemons []string) {
	on := func(r *Role) bool { return r != nil && strings.HasPrefix(r.DeviceType, "HB-RF-ETH@") }
	if on(p.HmRF) {
		hmrf = p.HmRF
	}
	if on(p.HmIP) && p.HmIPServerHmIP {
		hmip = p.HmIP
	}
	if hmrf == nil && hmip == nil {
		return nil, nil, nil
	}
	node := ""
	if hmrf != nil {
		node = hmrf.Node
	} else {
		node = hmip.Node
	}
	if p.Multimacd.Run && p.Multimacd.Node == node {
		daemons = append(daemons, "multimacd")
	}
	if hmrf != nil && p.RFD.Run {
		daemons = append(daemons, "rfd")
	}
	if hmip != nil && p.HmIPServer.Run {
		daemons = append(daemons, "hmipserver")
	}
	return hmrf, hmip, daemons
}
