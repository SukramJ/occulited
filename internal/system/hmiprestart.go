package system

import "sync"

// HmIPRestartLock is the one lock for everything that rewrites hmipserver's configuration and
// restarts it (openccu-lite B-198): local key mode's switches, the adapter exchange's retry and
// fresh start, and the device keys' apply. Before it, the apply waited for a switch but a switch
// did not wait for the apply, so one could restart hmipserver while the other was still writing
// its files. The holder names what runs; the second caller is refused with that name (a `409`),
// as the busy cases before it were. It is held from the first write until the restart returned,
// not during the check that follows a switch.
type HmIPRestartLock struct {
	mu     sync.Mutex
	holder string
}

// Acquire takes the lock for what (e.g. "a local key switch"); when it is held, held names the
// holder and ok is false.
func (l *HmIPRestartLock) Acquire(what string) (held string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.holder != "" {
		return l.holder, false
	}
	l.holder = what
	return "", true
}

// Release gives the lock back.
func (l *HmIPRestartLock) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.holder = ""
}

// Holder names what holds the lock, "" when nothing does.
func (l *HmIPRestartLock) Holder() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.holder
}
