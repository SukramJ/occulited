package syslogfwd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseEntry(t *testing.T) {
	e, ok := ParseEntry([]byte(`{"MESSAGE":"hello","PRIORITY":"3","BIN":[104,105,255],"TWICE":["a","b"],"EMPTY":[],"N":5}`))
	if !ok {
		t.Fatal("not parsed")
	}
	if e["MESSAGE"] != "hello" || e["PRIORITY"] != "3" || e["BIN"] != "hi�" || e["TWICE"] != "a" || e["EMPTY"] != "" {
		t.Errorf("%q", e)
	}
	if _, ok := e["N"]; ok {
		t.Error("a number is not a journal field value")
	}
	if _, ok := ParseEntry([]byte(`not json`)); ok {
		t.Error("parsed garbage")
	}
}

func TestParseLogHost(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"log.example.net", "log.example.net:514"},
		{" 192.0.2.7 ", "192.0.2.7:514"},
		{"192.0.2.7:5514", "192.0.2.7:5514"},
		{"fd00::1", "[fd00::1]:514"},
		{"[fd00::1]:5514", "[fd00::1]:5514"},
		{"[fd00::1]", "[fd00::1]:514"},
	} {
		got, err := ParseLogHost(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%q: %q %v, want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "host:0", "host:65536", "host:x", "a b", ":514", "fd00::zz"} {
		if got, err := ParseLogHost(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func withZone(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("CEST", 2*3600)
	t.Cleanup(func() { time.Local = old })
}

func TestFormat(t *testing.T) {
	withZone(t)
	// 2026-09-12T13:30:05.123456Z
	us := time.Date(2026, 9, 12, 13, 30, 5, 123456000, time.UTC).UnixMicro()
	e := Entry{
		"__REALTIME_TIMESTAMP": fmt.Sprint(us), "_HOSTNAME": "openccu-lite-b", "SYSLOG_IDENTIFIER": "rfd",
		"_PID": "812", "PRIORITY": "4", "SYSLOG_FACILITY": "3", "MESSAGE": "Device not reachable\n",
	}
	got := string(Format(e, "fallback", DefaultMaxSize))
	want := "<28>1 2026-09-12T15:30:05.123456+02:00 openccu-lite-b rfd 812 - - Device not reachable"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}

	// the kernel: facility 0; no priority: info; no host: the fallback; SYSLOG_PID wins over _PID
	e = Entry{"_TRANSPORT": "kernel", "SYSLOG_IDENTIFIER": "kernel", "MESSAGE": "usb 1-1: new device"}
	if got := string(Format(e, "box", 0)); got != "<6>1 - box kernel - - - usb 1-1: new device" {
		t.Errorf("kernel: %q", got)
	}
	// a native entry without a facility: user; the identifier falls back to _COMM
	e = Entry{"_COMM": "occulited", "SYSLOG_PID": "7", "_PID": "9", "PRIORITY": "3", "MESSAGE": "x"}
	if got := string(Format(e, "", 0)); got != "<11>1 - - occulited 7 - - x" {
		t.Errorf("native: %q", got)
	}
	// header fields: a space or a non-ASCII character becomes _, the app name is at most 48
	long := strings.Repeat("a", 60)
	e = Entry{"SYSLOG_IDENTIFIER": "addon install " + long, "_HOSTNAME": "bäum", "MESSAGE": ""}
	got = string(Format(e, "", 0))
	if want := "<14>1 - b_um " + ("addon_install_" + long)[:48] + " - - -"; got != want {
		t.Errorf("headers:\n got %q\nwant %q", got, want)
	}
	// a bad priority or facility is the default
	e = Entry{"PRIORITY": "9", "SYSLOG_FACILITY": "24", "MESSAGE": "m"}
	if got := string(Format(e, "h", 0)); !strings.HasPrefix(got, "<14>1 ") {
		t.Errorf("defaults: %q", got)
	}
	// the whole message is cut at a character boundary
	e = Entry{"SYSLOG_IDENTIFIER": "t", "MESSAGE": strings.Repeat("ü", 100)}
	got = string(Format(e, "h", 40))
	if len(got) > 40 || !strings.HasPrefix(got, "<14>1 - h t - - - ü") || strings.ContainsRune(got, '�') {
		t.Errorf("cut: %d %q", len(got), got)
	}
}

func TestReadLine(t *testing.T) {
	long := strings.Repeat("x", 300)
	rd := bufio.NewReaderSize(strings.NewReader("short\n"+long+"\nnext\nlast"), 16)
	var got []string
	for {
		line, err := readLine(rd, 100)
		got = append(got, strings.TrimRight(string(line), "\n"))
		if err != nil {
			break
		}
	}
	if fmt.Sprint(got) != "[short  next last]" {
		t.Errorf("%q", got)
	}
}

// entry is one journalctl -o json line
func entry(cursor string, monoMin int, msg string) string {
	return fmt.Sprintf(`{"__CURSOR":%q,"__MONOTONIC_TIMESTAMP":"%d","SYSLOG_IDENTIFIER":"t","PRIORITY":"6","MESSAGE":%q}`, cursor, int64(monoMin)*60e6, msg)
}

type sink struct {
	mu   sync.Mutex
	msgs []string
	fail int // the next sends that fail
}

func (s *sink) send(b []byte, _ *net.UDPAddr) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail > 0 {
		s.fail--
		return errors.New("network is unreachable")
	}
	s.msgs = append(s.msgs, string(b))
	return nil
}

func (s *sink) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.msgs)
}

func newForwarder(t *testing.T, input string, s *sink) (*Forwarder, *[]string) {
	t.Helper()
	var args []string
	f := &Forwarder{
		LogHost:    "192.0.2.7:5514",
		CursorFile: filepath.Join(t.TempDir(), "cursor"),
		Hostname:   "box",
		Stream: func(_ context.Context, name string, a ...string) (io.ReadCloser, error) {
			args = append([]string{name}, a...)
			return io.NopCloser(strings.NewReader(input)), nil
		},
		Resolve: func(_ context.Context, hp string) (*net.UDPAddr, error) {
			return net.ResolveUDPAddr("udp", hp)
		},
		Send:     s.send,
		Uptime:   func() (time.Duration, bool) { return 30 * time.Minute, true },
		RetryMin: time.Millisecond,
		RetryMax: 2 * time.Millisecond,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return f, &args
}

func msgs(list []string) []string {
	out := make([]string, len(list))
	for i, m := range list {
		out[i] = m[strings.LastIndex(m, " ")+1:]
	}
	return out
}

func TestRunWithoutCursor(t *testing.T) {
	s := &sink{}
	in := strings.Join([]string{entry("s=1", 1, "boot"), entry("s=2", 19, "old"), "garbage", entry("s=3", 20, "recent"), entry("s=4", 25, "newest")}, "\n") + "\n"
	f, args := newForwarder(t, in, s)
	err := f.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "journalctl ended") {
		t.Fatalf("a journalctl that ends is an error for the unit to restart: %v", err)
	}
	if strings.Join(*args, " ") != "journalctl -f -o json --no-pager -q -b -n all" {
		t.Errorf("args %q", *args)
	}
	// uptime 30 min, backlog 10 min: what is older than minute 20 of the boot is not sent
	if fmt.Sprint(msgs(s.got())) != "[recent newest]" {
		t.Errorf("sent %q", s.got())
	}
	if b, _ := os.ReadFile(f.CursorFile); string(b) != "s=4\n" {
		t.Errorf("cursor %q", b)
	}
}

func TestRunEarlyInTheBootSendsTheWholeBoot(t *testing.T) {
	s := &sink{}
	f, _ := newForwarder(t, entry("s=1", 0, "first")+"\n"+entry("s=2", 3, "second")+"\n", s)
	f.Uptime = func() (time.Duration, bool) { return 4 * time.Minute, true }
	_ = f.Run(context.Background())
	if fmt.Sprint(msgs(s.got())) != "[first second]" {
		t.Errorf("sent %q", s.got())
	}
}

func TestRunContinuesAtItsCursor(t *testing.T) {
	s := &sink{}
	f, args := newForwarder(t, entry("s=9", 1, "old-but-after-the-cursor")+"\n", s)
	if err := os.WriteFile(f.CursorFile, []byte("s=8;i=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = f.Run(context.Background())
	if strings.Join(*args, " ") != "journalctl -f -o json --no-pager -q --after-cursor=s=8;i=2" {
		t.Errorf("args %q", *args)
	}
	// after a cursor nothing is skipped by age
	if fmt.Sprint(msgs(s.got())) != "[old-but-after-the-cursor]" {
		t.Errorf("sent %q", s.got())
	}
	// a cursor file that is not a cursor is a start without one
	_ = os.WriteFile(f.CursorFile, []byte("--since=yesterday\n"), 0o600)
	_ = f.Run(context.Background())
	if slices.Contains(*args, "--since=yesterday") || !slices.Contains(*args, "-b") {
		t.Errorf("args %q", *args)
	}
}

func TestRunRetriesAFailingSend(t *testing.T) {
	s := &sink{fail: 3}
	in := entry("s=1", 25, "one") + "\n" + entry("s=2", 26, "two") + "\n"
	f, _ := newForwarder(t, in, s)
	resolved := 0
	f.Resolve = func(_ context.Context, hp string) (*net.UDPAddr, error) {
		resolved++
		return net.ResolveUDPAddr("udp", hp)
	}
	_ = f.Run(context.Background())
	if fmt.Sprint(msgs(s.got())) != "[one two]" {
		t.Errorf("sent %q", s.got())
	}
	// once at the start, once after every failure
	if resolved != 4 {
		t.Errorf("resolved %d times", resolved)
	}
}

func TestRunStopsWhileFailingAndKeepsItsCursor(t *testing.T) {
	s := &sink{}
	pr, pw := io.Pipe()
	f, _ := newForwarder(t, "", s)
	f.Stream = func(ctx context.Context, _ string, _ ...string) (io.ReadCloser, error) {
		go func() { <-ctx.Done(); _ = pw.CloseWithError(ctx.Err()) }()
		return pr, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()
	_, _ = io.WriteString(pw, entry("s=1", 25, "sent")+"\n")
	deadline := time.Now().Add(5 * time.Second)
	for len(s.got()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	s.mu.Lock()
	s.fail = 1 << 30
	s.mu.Unlock()
	_, _ = io.WriteString(pw, entry("s=2", 26, "never")+"\n")
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a stop is not an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if b, _ := os.ReadFile(f.CursorFile); string(b) != "s=1\n" {
		t.Errorf("the cursor is the last entry sent: %q", b)
	}
}

func TestRunWaitsForTheName(t *testing.T) {
	s := &sink{}
	f, _ := newForwarder(t, entry("s=1", 25, "one")+"\n", s)
	f.LogHost = "log.example.net"
	tries := 0
	f.Resolve = func(_ context.Context, hp string) (*net.UDPAddr, error) {
		if tries++; tries < 3 {
			return nil, errors.New("no such host")
		}
		if hp != "log.example.net:514" {
			t.Errorf("resolving %q", hp)
		}
		return &net.UDPAddr{IP: net.ParseIP("192.0.2.9"), Port: 514}, nil
	}
	_ = f.Run(context.Background())
	if tries != 3 || len(s.got()) != 1 {
		t.Errorf("tries %d sent %q", tries, s.got())
	}
	// a stop while waiting for the name is not an error
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.Resolve = func(context.Context, string) (*net.UDPAddr, error) { return nil, errors.New("no such host") }
	if err := f.Run(ctx); err != nil {
		t.Errorf("%v", err)
	}
	// a LOGHOST that is not one is an error
	f.LogHost = "a b"
	if err := f.Run(context.Background()); err == nil {
		t.Error("bad LOGHOST accepted")
	}
}

func TestSendOverUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback UDP:", err)
	}
	defer pc.Close()
	f := &Forwarder{}
	defer f.closeConns()
	to := pc.LocalAddr().(*net.UDPAddr)
	if err := f.sendTo([]byte("<14>1 - h t - - - hi"), to); err != nil {
		t.Fatal(err)
	}
	_ = pc.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 100)
	n, _, err := pc.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "<14>1 - h t - - - hi" {
		t.Errorf("%q %v", buf[:n], err)
	}
}
