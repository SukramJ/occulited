package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/sshkeys"
	"github.com/hobbyquaker/occulited/internal/system"
)

const (
	sshTestKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEFSx3g/oLwaC84xZMOZIv9tk8m/eImdz0UxBwEjrnMj laptop ed"
	sshTestFP  = "SHA256:mZ+3k7WSTBEIONtZ4sWyKZdRbsGKX4yQvqbX/7pwwqs"
	sshTestRSA = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCOvN1TaNOxNdQnBUpQMsOrfT0RjylvbY+YKOMTo5UoHOyscnMCADCShnMc4fOdwCcSkzTGpv++PiyVgxgzlBqks7DQp1AG9thxFEXwKpPGv0s4fOrTFLBaUGY59vz6nrEUBMt+OHk53ED8jcBoN7prgte56e8bgIcDHgFJKgFLOW0c5zEZjX3Y2kuRywCOVD3aDccmQCuT15fTaBHb44f/dx2JL7nQQbkcKKoYg9gsq2tw7mugsh9f0SbpJICbh8eRggBVV1RMPEhRy/QEAgb4CtrzOuNXlt2FFb48u0vRjGrYlY+hs5BQuM05N/2jMB0QvS7lO4fSEBEvQvd94RpRqANdnsgQBnAf0ZpjecIh8BisjprwNzQ4ey3/YFAywZWoTFUAxwnCnU9yi5MUM/+eW7to/ESsWMBN/wft5IxsnRm9JX3qgW6et8LWReA14wku/ibt1zBAsc2WcgKAVIMCA54i+U8dVJqcr2dz3kEgBMWcJdUL4JlVZlcomKaNCeM= rsa3072"
	sshLabKey  = "ecdsa-sha2-nistp384 AAAAE2VjZHNhLXNoYTItbmlzdHAzODQAAAAIbmlzdHAzODQAAABhBPUSfBJXk7J2BPZi3b7iRP74eM1MdcEXFedN6chSVTyF/9aDxY1p7bBOHauaCNJAr8v/NTm3IalSCRmjTG/Npl7TxfRK0yehzV9v+hQe5qbKdfpLnVSd9f3J/45iXcD/xg== lab"
)

// sshHelper is the helper's SSH side in memory. Programs it refuses, every one, as the helper
// does with any program not on its list: mkpasswd among them (openccu-lite task 302).
type sshHelper struct {
	priv.Local
	root        string // the fake root the API serves
	file        []byte
	ended       []int
	keyOnly     bool
	keyOnlySets int
	mu          sync.Mutex
	runs        []string
}

func (h *sshHelper) Run(_ context.Context, name string, args []string, _ []byte) (priv.Result, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs = append(h.runs, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return priv.Result{Exit: 1, Stderr: []byte("refused run " + name)}, nil
}

func (h *sshHelper) Runs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.runs...)
}

func (h *sshHelper) ReadAuthorizedKeys() ([]byte, error) { return h.file, nil }
func (h *sshHelper) WriteAuthorizedKeys(lines []string) error {
	var keys []sshkeys.Key
	for _, l := range lines {
		k, err := sshkeys.Parse(l)
		if err != nil {
			return err
		}
		keys = append(keys, k)
	}
	h.file = sshkeys.WithSection(h.file, keys)
	return nil
}
func (h *sshHelper) EndSSHSession(pid int) error { h.ended = append(h.ended, pid); return nil }
func (h *sshHelper) SSHKeyOnly() (bool, error)   { return h.keyOnly, nil }
func (h *sshHelper) SetSSHKeyOnly(on bool) error { h.keyOnly = on; h.keyOnlySets++; return nil }

func sshAPIServer(t *testing.T) (*httptest.Server, *sshHelper, map[string]string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	store, err := auth.Open(dir, auth.Options{SessionDir: filepath.Join(dir, "run", "sessions")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser("bob", "bobsecret1", auth.RoleUser, false); err != nil {
		t.Fatal(err)
	}
	h := &sshHelper{file: []byte(sshLabKey + "\n")}
	old := system.Priv
	system.Priv = h
	t.Cleanup(func() { system.Priv = old })

	// two sessions: one from this test's address (the page's own), one from elsewhere
	root := t.TempDir()
	h.root = root
	proc := func(pid, ppid int, comm, title string) {
		d := filepath.Join(root, "proc", strconv.Itoa(pid))
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(d, "cmdline"), []byte(title+"\x00"), 0o644)
		_ = os.WriteFile(filepath.Join(d, "status"), []byte("PPid:\t"+strconv.Itoa(ppid)+"\n"), 0o644)
	}
	proc(700, 1, "sshd-session", "sshd-session: root [postauth]")
	proc(701, 700, "sshd-session", "sshd-session: root@pts/0")
	proc(800, 1, "sshd-session", "sshd-session: root [postauth]")
	proc(801, 800, "sshd-session", "sshd-session: root@notty")
	journal := `{"_PID":"700","MESSAGE":"Accepted password for root from 127.0.0.1 port 40000 ssh2","__REALTIME_TIMESTAMP":"1789840000000000"}
{"_PID":"800","MESSAGE":"Accepted publickey for root from 192.0.2.114 port 46139 ssh2: ED25519 SHA256:x","__REALTIME_TIMESTAMP":"1789841000000000"}
`
	run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "journalctl" {
			return []byte(journal), nil
		}
		return nil, nil
	}
	mux := http.NewServeMux()
	a := &AuthAPI{Store: store}
	a.Register(mux)
	(&SystemAPI{Root: system.Root(root), Journal: &system.JournalLog{Run: run}, ConfirmTicket: store.RedeemConfirmed}).Register(mux)
	srv := httptest.NewServer(a.Middleware(mux))
	t.Cleanup(srv.Close)
	admin := map[string]string{"Cookie": CookieName + "=" + cookieOf(t, srv, `{"username":"admin","password":"secret123"}`)}
	bob := map[string]string{"Cookie": CookieName + "=" + cookieOf(t, srv, `{"username":"bob","password":"bobsecret1"}`)}
	return srv, h, admin, bob
}

func sshConfirm(t *testing.T, srv *httptest.Server, hdr map[string]string, path string) map[string]string {
	t.Helper()
	st, out, _ := do(t, srv, "POST", "/api/auth/v1/ticket", `{"path":"`+path+`","confirm":true,"password":"secret123"}`, hdr)
	tk, _ := out["ticket"].(string)
	if st != 200 || tk == "" {
		t.Fatalf("confirm: %d %v", st, out)
	}
	with := map[string]string{"X-Occulite-Confirm": tk}
	for k, v := range hdr {
		with[k] = v
	}
	return with
}

// task 185: adding a key asks for the password every time; a broken key is refused before the
// password is spent; a key outside the section is listed and cannot be removed
func TestSSHKeysOverHTTP(t *testing.T) {
	srv, h, admin, bob := sshAPIServer(t)
	body := `{"key":"` + sshTestKey + `"}`
	if st, out, _ := do(t, srv, "POST", SSHKeysPath, body, admin); st != 403 || out["error"] != "confirm-required" {
		t.Fatalf("without a confirmation: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "POST", SSHKeysPath, `{"key":"ssh-rsa AAAA"}`, admin); st != 422 {
		t.Errorf("a broken key: %d", st)
	}
	if st, _, _ := do(t, srv, "POST", SSHKeysPath, body, bob); st != 403 {
		t.Errorf("a user added a key: %d", st)
	}
	confirmed := sshConfirm(t, srv, admin, SSHKeysPath)
	if st, out, _ := do(t, srv, "POST", SSHKeysPath, body, confirmed); st != 201 || out["fingerprint"] != sshTestFP {
		t.Fatalf("add: %d %v", st, out)
	}
	// the ticket is spent
	if st, _, _ := do(t, srv, "POST", SSHKeysPath, `{"key":"`+sshTestRSA+`"}`, confirmed); st != 403 {
		t.Errorf("a ticket used twice: %d", st)
	}
	// a key the file holds already is said before the password is asked: in the section, and outside it
	if st, out, _ := do(t, srv, "POST", SSHKeysPath, body, admin); st != 409 {
		t.Errorf("twice: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", SSHKeysPath, `{"key":"`+sshLabKey+`"}`, admin); st != 409 {
		t.Errorf("the lab's key: %d %v", st, out)
	}
	if !strings.HasPrefix(string(h.file), sshLabKey+"\n") {
		t.Errorf("the lab's line: %q", h.file)
	}
	st, out, _ := do(t, srv, "GET", SSHKeysPath, "", admin)
	managed, _ := out["managed"].([]any)
	other, _ := out["other"].([]any)
	if st != 200 || len(managed) != 1 || len(other) != 1 {
		t.Fatalf("list: %d %v", st, out)
	}
	labFP := other[0].(map[string]any)["fingerprint"].(string)
	if st, _, _ := do(t, srv, "DELETE", SSHKeysPath+"?fingerprint="+url.QueryEscape(labFP), "", admin); st != 404 {
		t.Errorf("removed a key outside the section: %d", st)
	}
	if st, _, _ := do(t, srv, "DELETE", SSHKeysPath+"?fingerprint="+url.QueryEscape(sshTestFP), "", bob); st != 403 {
		t.Errorf("a user removed a key: %d", st)
	}
	if st, _, _ := do(t, srv, "DELETE", SSHKeysPath+"?fingerprint="+url.QueryEscape(sshTestFP), "", admin); st != 200 {
		t.Errorf("remove: %d", st)
	}
	if string(h.file) != sshLabKey+"\n" {
		t.Errorf("after the removal: %q", h.file)
	}
}

// root's password asks for the user's password too, every time
func TestSSHPasswordAsksForThePassword(t *testing.T) {
	srv, _, admin, _ := sshAPIServer(t)
	if st, out, _ := do(t, srv, "POST", SSHPasswordPath, `{"password":"rootpass123"}`, admin); st != 403 || out["error"] != "confirm-required" {
		t.Errorf("without a confirmation: %d %v", st, out)
	}
	// a ticket for the keys is no ticket for the password
	if st, _, _ := do(t, srv, "POST", SSHPasswordPath, `{"password":"rootpass123"}`, sshConfirm(t, srv, admin, SSHKeysPath)); st != 403 {
		t.Errorf("the keys' ticket set the password: %d", st)
	}
}

// fakeMkpasswd puts a shell script named mkpasswd first on PATH that records its stdin and
// prints one hash: what the daemon's own mkpasswd run was handed becomes visible.
func fakeMkpasswd(t *testing.T, body string) (stdinFile string) {
	t.Helper()
	dir := t.TempDir()
	stdinFile = filepath.Join(dir, "stdin")
	script := "#!/bin/sh\ncat > " + stdinFile + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "mkpasswd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stdinFile
}

// openccu-lite task 302 (GitHub #2): the route as production wires it - no hasher substituted,
// a helper that refuses every program - hashes the typed password in the daemon's own process
// and sets that hash. Before, the hash was asked of the helper through the Runner, which has no
// mkpasswd ("refused by the privilege helper: program mkpasswd") and carries no stdin, so even an
// allowed mkpasswd would have hashed an empty input.
func TestSSHPasswordOverHTTP(t *testing.T) {
	srv, h, admin, bob := sshAPIServer(t)
	shadow := filepath.Join(h.root, "etc", "config", "shadow")
	before := "root:*:19000:0:99999:7:::\nhm:*:19000:0:99999:7:::\n"
	if err := os.MkdirAll(filepath.Dir(shadow), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shadow, []byte(before), 0o640); err != nil {
		t.Fatal(err)
	}
	stdinFile := fakeMkpasswd(t, `echo '$6$saltsalt$hashofthetypedpassword'`)

	if st, _, _ := do(t, srv, "POST", SSHPasswordPath, `{"password":"rootpass123"}`, sshConfirm(t, srv, admin, SSHPasswordPath)); st != 200 {
		t.Fatalf("set: %d", st)
	}
	if got, _ := os.ReadFile(stdinFile); string(got) != "rootpass123\n" {
		t.Errorf("mkpasswd read %q, not the typed password", got)
	}
	if got, _ := os.ReadFile(shadow); !strings.HasPrefix(string(got), "root:$6$saltsalt$hashofthetypedpassword:19000:") || !strings.Contains(string(got), "\nhm:*:19000:") {
		t.Errorf("shadow: %q", got)
	}
	if runs := h.Runs(); len(runs) != 0 {
		t.Errorf("the route went through the privilege helper: %v", runs)
	}
	// a user's confirmation is not an admin's
	if st, _, _ := do(t, srv, "POST", SSHPasswordPath, `{"password":"rootpass123"}`, bob); st != 403 {
		t.Errorf("a user set root's password: %d", st)
	}
	// too short: refused before any hashing, with the confirmation spent
	if st, out, _ := do(t, srv, "POST", SSHPasswordPath, `{"password":"short"}`, sshConfirm(t, srv, admin, SSHPasswordPath)); st != 400 || out["error"] != "invalid" {
		t.Errorf("short: %d %v", st, out)
	}
	// mkpasswd failing: 400, and the hash set before stays
	fakeMkpasswd(t, `echo 'mkpasswd: boom' >&2; exit 1`)
	if st, out, _ := do(t, srv, "POST", SSHPasswordPath, `{"password":"anotherpass1"}`, sshConfirm(t, srv, admin, SSHPasswordPath)); st != 400 || out["error"] != "invalid" || !strings.Contains(out["message"].(string), "exit 1") {
		t.Errorf("a failing mkpasswd: %d %v", st, out)
	}
	if got, _ := os.ReadFile(shadow); !strings.HasPrefix(string(got), "root:$6$saltsalt$hashofthetypedpassword:19000:") {
		t.Errorf("a failed hash changed shadow: %q", got)
	}
}

func TestSSHSessionsOverHTTP(t *testing.T) {
	srv, h, admin, bob := sshAPIServer(t)
	st, out, _ := do(t, srv, "GET", "/api/system/v1/ssh/sessions", "", admin)
	list, _ := out["sessions"].([]any)
	if st != 200 || len(list) != 2 {
		t.Fatalf("%d %v", st, out)
	}
	mine, other := list[0].(map[string]any), list[1].(map[string]any)
	if mine["own"] != true || mine["tty"] != "pts/0" || mine["method"] != "password" || other["own"] != nil || other["from"] != "192.0.2.114" {
		t.Errorf("%v\n%v", mine, other)
	}
	if st, _, _ := do(t, srv, "DELETE", "/api/system/v1/ssh/sessions/800", "", bob); st != 403 {
		t.Errorf("a user ended a session: %d", st)
	}
	for _, id := range []string{"801", "1", "x"} {
		if st, _, _ := do(t, srv, "DELETE", "/api/system/v1/ssh/sessions/"+id, "", admin); st != 404 && st != 422 {
			t.Errorf("%s: %d", id, st)
		}
	}
	if st, _, _ := do(t, srv, "DELETE", "/api/system/v1/ssh/sessions/800", "", admin); st != 200 || len(h.ended) != 1 || h.ended[0] != 800 {
		t.Errorf("end: %d %v", st, h.ended)
	}
}

// openccu-lite task 245: only key login - the SSH view says it, switching it on is refused while
// root has no key, and the last key is not removed while it is on.
func TestSSHKeyOnlyOverHTTP(t *testing.T) {
	srv, h, admin, bob := sshAPIServer(t)
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/ssh", "", admin); out["key_only"] != false {
		t.Fatalf("%v", out)
	}
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/ssh/key-only", `{"on":true}`, bob); st != 403 {
		t.Fatalf("a user: %d", st)
	}
	// no key at all: refused
	h.file = nil
	if st, out, _ := do(t, srv, "PUT", "/api/system/v1/ssh/key-only", `{"on":true}`, admin); st != 409 || out["error"] != "no-key" || h.keyOnlySets != 0 {
		t.Fatalf("no key: %d %v", st, out)
	}
	// one key of the page's section: on
	k, _ := sshkeys.Parse(sshTestRSA)
	h.file = sshkeys.WithSection(nil, []sshkeys.Key{k})
	if st, out, _ := do(t, srv, "PUT", "/api/system/v1/ssh/key-only", `{"on":true}`, admin); st != 200 || out["key_only"] != true || !h.keyOnly {
		t.Fatalf("on: %d %v", st, out)
	}
	// the last key: not removed while it is on
	if st, out, _ := do(t, srv, "DELETE", SSHKeysPath+"?fingerprint="+url.QueryEscape(k.Fingerprint), "", admin); st != 409 || out["error"] != "last-key" {
		t.Fatalf("last key: %d %v", st, out)
	}
	// off: the key may go
	if st, out, _ := do(t, srv, "PUT", "/api/system/v1/ssh/key-only", `{"on":false}`, admin); st != 200 || out["key_only"] != false {
		t.Fatalf("off: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "DELETE", SSHKeysPath+"?fingerprint="+url.QueryEscape(k.Fingerprint), "", admin); st != 200 {
		t.Fatalf("remove: %d", st)
	}
}
