package system

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/sshkeys"
)

// Task 185: the Remote access page's SSH - root's keys and the sessions open now.

// sshPriv is what the page needs of the helper: root's authorized_keys and ending a session.
func sshPriv() (priv.SSHOps, error) {
	p, ok := Priv.(priv.SSHOps)
	if !ok {
		return nil, errors.New("the privilege helper has no SSH operations")
	}
	return p, nil
}

// keysMu makes a change of the keys one read and one write: two pastes at once must not drop one.
var keysMu sync.Mutex

// SSHKeys reads root's authorized_keys: the keys of occulited's section and the others.
func SSHKeys() (sshkeys.File, error) {
	p, err := sshPriv()
	if err != nil {
		return sshkeys.File{}, err
	}
	b, err := p.ReadAuthorizedKeys()
	if err != nil {
		return sshkeys.File{}, err
	}
	return sshkeys.Read(b), nil
}

// ErrKeyPresent is a key the file holds already, in the section or outside it.
var ErrKeyPresent = errors.New("this key is already there")

// ErrKeyNotManaged is a key the section does not hold: one added outside the page, or none.
var ErrKeyNotManaged = errors.New("no key of the page has this fingerprint")

// AddSSHKey puts a pasted key into the section (sshkeys.Parse decides what is taken).
func AddSSHKey(line string) (sshkeys.Key, error) {
	k, err := sshkeys.Parse(line)
	if err != nil {
		return sshkeys.Key{}, err
	}
	keysMu.Lock()
	defer keysMu.Unlock()
	f, err := SSHKeys()
	if err != nil {
		return sshkeys.Key{}, err
	}
	lines := []string{}
	for _, m := range append(append([]sshkeys.Key{}, f.Managed...), f.Other...) {
		if m.Fingerprint == k.Fingerprint {
			return sshkeys.Key{}, ErrKeyPresent
		}
	}
	for _, m := range f.Managed {
		lines = append(lines, m.Line())
	}
	p, _ := sshPriv()
	return k, p.WriteAuthorizedKeys(append(lines, k.Line()))
}

// Task 245: root's password login can be refused, so SSH takes a key only. The guard against a
// lock-out: it is switched on only while root has a key, and the last key is not removed while
// it is on (the web interface and the console stay a way back in either way).

// ErrNoSSHKey refuses key-only login without a key to log in with.
var ErrNoSSHKey = errors.New("add a key first: with only key login and no key, nobody can log in over SSH")

// ErrLastSSHKey refuses to remove the last key while only key login is on.
var ErrLastSSHKey = errors.New("this is root's last key and only key login is on: switch only key login off first")

// SSHKeyOnly says whether sshd refuses root's password.
func SSHKeyOnly() (bool, error) {
	p, err := sshPriv()
	if err != nil {
		return false, err
	}
	return p.SSHKeyOnly()
}

// SetSSHKeyOnly switches it; on needs a key in root's authorized_keys (the page's or another).
func SetSSHKeyOnly(on bool) error {
	keysMu.Lock()
	defer keysMu.Unlock()
	p, err := sshPriv()
	if err != nil {
		return err
	}
	if on {
		f, err := SSHKeys()
		if err != nil {
			return err
		}
		if len(f.Managed)+len(f.Other) == 0 {
			return ErrNoSSHKey
		}
	}
	return p.SetSSHKeyOnly(on)
}

// RemoveSSHKey takes a key of the section out; a key outside it is not the page's to remove.
func RemoveSSHKey(fingerprint string) (sshkeys.Key, error) {
	keysMu.Lock()
	defer keysMu.Unlock()
	f, err := SSHKeys()
	if err != nil {
		return sshkeys.Key{}, err
	}
	if len(f.Managed)+len(f.Other) == 1 {
		if p, err := sshPriv(); err == nil {
			if on, err := p.SSHKeyOnly(); err == nil && on {
				for _, m := range f.Managed {
					if m.Fingerprint == fingerprint {
						return sshkeys.Key{}, ErrLastSSHKey
					}
				}
			}
		}
	}
	var gone *sshkeys.Key
	lines := []string{}
	for i, m := range f.Managed {
		if m.Fingerprint == fingerprint && gone == nil {
			gone = &f.Managed[i]
			continue
		}
		lines = append(lines, m.Line())
	}
	if gone == nil {
		return sshkeys.Key{}, ErrKeyNotManaged
	}
	p, _ := sshPriv()
	return *gone, p.WriteAuthorizedKeys(lines)
}

// SSHSession is one SSH connection that has logged in.
type SSHSession struct {
	// ID is the connection's privileged sshd-session process, the one the page ends it by.
	ID   int    `json:"id"`
	User string `json:"user"`
	// TTY is pts/N for a terminal; empty for a command or a file copy (sshd's "notty").
	TTY    string `json:"tty,omitempty"`
	From   string `json:"from,omitempty"`
	Port   int    `json:"port,omitempty"`
	Since  string `json:"since,omitempty"`  // RFC 3339: when it logged in
	Method string `json:"method,omitempty"` // publickey, password, keyboard-interactive
	// KeyType and KeyFingerprint are the key a publickey login used (ED25519, SHA256:…).
	KeyType        string `json:"key_type,omitempty"`
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
}

// accepted is sshd's line for a login: "Accepted publickey for root from 192.0.2.114 port 46139
// ssh2: ED25519 SHA256:…" - the password form ends after ssh2.
var accepted = regexp.MustCompile(`^Accepted (\S+) for (\S+) from (\S+) port (\d+) ssh2(?:: (\S+) (\S+))?`)

// sessionTitle is the title an sshd-session process of a logged-in connection sets on its child:
// "sshd-session: root@pts/0" or "sshd-session: root@notty".
var sessionTitle = regexp.MustCompile(`^sshd-session: (\S+)@(\S+)`)

// SSHSessions lists the SSH connections open now (run: see sshLogins). /proc says which exist - each is an
// sshd-session process whose child is titled user@tty - and sshd's "Accepted" line in this boot's
// journal, which that process logged, says from where, how and since when.
func (r Root) SSHSessions(ctx context.Context, run Runner) ([]SSHSession, error) {
	procs, err := os.ReadDir(r.join("/proc"))
	if err != nil {
		return nil, err
	}
	byParent := map[int]SSHSession{}
	for _, e := range procs {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		base := r.join("/proc/" + e.Name())
		comm, _ := os.ReadFile(base + "/comm")
		if strings.TrimSpace(string(comm)) != "sshd-session" {
			continue
		}
		cmd, _ := os.ReadFile(base + "/cmdline")
		m := sessionTitle.FindStringSubmatch(strings.TrimRight(strings.ReplaceAll(string(cmd), "\x00", " "), " "))
		if m == nil {
			continue
		}
		ppid := parentOf(base + "/status")
		if ppid <= 1 {
			continue
		}
		s := SSHSession{ID: ppid, User: m[1]}
		if m[2] != "notty" {
			s.TTY = m[2]
		}
		byParent[ppid] = s
	}
	out := make([]SSHSession, 0, len(byParent))
	if len(byParent) == 0 {
		return out, nil
	}
	logins := r.sshLogins(ctx, run)
	for id, s := range byParent {
		if l, ok := logins[id]; ok {
			s.From, s.Port, s.Since, s.Method, s.KeyType, s.KeyFingerprint = l.From, l.Port, l.Since, l.Method, l.KeyType, l.KeyFingerprint
			if s.User == "" {
				s.User = l.User
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Since != out[j].Since {
			return out[i].Since < out[j].Since
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func parentOf(status string) int {
	b, err := os.ReadFile(status)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PPid:"); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}

// sshLogins reads this boot's "Accepted" lines by the pid that logged them. An older sshd logged
// them as sshd, a newer one as sshd-session; both are asked for. journalctl runs as occulited, which
// reads the journal (the systemd-journal group), as the Log page does - not through the helper,
// whose program list has no journalctl; run is a test's stand-in, nil runs journalctl.
func (r Root) sshLogins(ctx context.Context, run Runner) map[int]SSHSession {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := append([]string{"-b", "-t", "sshd-session", "-t", "sshd", "-o", "json", "--no-pager", "-q", "-g", "^Accepted "}, JournalFileArgs(r)...)
	var out []byte
	var err error
	if run != nil {
		out, err = run(ctx, "journalctl", args...)
	} else {
		out, err = exec.CommandContext(ctx, "journalctl", args...).Output()
	}
	logins := map[int]SSHSession{}
	if err != nil {
		return logins
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		var e map[string]any
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		msg, _ := e["MESSAGE"].(string)
		pidS, _ := e["_PID"].(string)
		pid, err := strconv.Atoi(pidS)
		m := accepted.FindStringSubmatch(msg)
		if err != nil || m == nil {
			continue
		}
		port, _ := strconv.Atoi(m[4])
		s := SSHSession{Method: m[1], User: m[2], From: m[3], Port: port, KeyType: m[5], KeyFingerprint: m[6]}
		if ts, _ := e["__REALTIME_TIMESTAMP"].(string); ts != "" {
			if us, err := strconv.ParseInt(ts, 10, 64); err == nil {
				s.Since = time.UnixMicro(us).Format(time.RFC3339)
			}
		}
		logins[pid] = s // a later line of the same pid (a reused one) wins
	}
	return logins
}

// ErrNoSession is an id that is not an open SSH connection.
var ErrNoSession = errors.New("no such SSH session")

// EndSSHSession ends one of SSHSessions' connections: its id must be one of them now - the page
// cannot end any other process through this, and the helper checks the process once more.
func (r Root) EndSSHSession(ctx context.Context, run Runner, id int) (SSHSession, error) {
	list, err := r.SSHSessions(ctx, run)
	if err != nil {
		return SSHSession{}, err
	}
	for _, s := range list {
		if s.ID == id {
			p, err := sshPriv()
			if err != nil {
				return s, err
			}
			if err := p.EndSSHSession(id); err != nil {
				return s, fmt.Errorf("end the session: %w", err)
			}
			return s, nil
		}
	}
	return SSHSession{}, ErrNoSession
}
