package journald

import (
	"bytes"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestEncode(t *testing.T) {
	for _, c := range []struct {
		name   string
		fields []Field
		want   string
	}{
		{"plain", []Field{{"MESSAGE", "hello"}, {"PRIORITY", "6"}}, "MESSAGE=hello\nPRIORITY=6\n"},
		{"empty value", []Field{{"MESSAGE", ""}}, "MESSAGE=\n"},
		{"an equals sign in the value", []Field{{"MESSAGE", "a=b"}}, "MESSAGE=a=b\n"},
		{"a newline: the binary form", []Field{{"MESSAGE", "one\ntwo"}, {"SYSLOG_IDENTIFIER", "x"}},
			"MESSAGE\n\x07\x00\x00\x00\x00\x00\x00\x00one\ntwo\nSYSLOG_IDENTIFIER=x\n"},
		{"keys journald would drop", []Field{{"_PID", "1"}, {"lower", "x"}, {"1ST", "x"}, {"", "x"}, {"OK_2", "y"}}, "OK_2=y\n"},
	} {
		if got := string(Encode(c.fields)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDecodeIsTheInverse(t *testing.T) {
	for _, fields := range [][]Field{
		{{"MESSAGE", "hello"}},
		{{"MESSAGE", "line one\nline two\n"}, {"ADDON_ID", "hm2mqtt"}, {"PRIORITY", "3"}},
		{{"MESSAGE", "=\n="}, {"X", ""}},
	} {
		got, err := Decode(Encode(fields))
		if err != nil || !reflect.DeepEqual(got, fields) {
			t.Errorf("%q: %q %v", fields, got, err)
		}
	}
	for _, bad := range []string{"MESSAGE=no newline", "MESSAGE\n\x05\x00", "MESSAGE\n\x09\x00\x00\x00\x00\x00\x00\x00short\n"} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
}

func TestFit(t *testing.T) {
	long := strings.Repeat("ä", 5000) // 10000 bytes, two per character
	for _, c := range []struct {
		name   string
		fields []Field
		max    int
	}{
		{"one long message", []Field{{"MESSAGE", long}, {"PRIORITY", "6"}}, 1000},
		{"a long multi-line message", []Field{{"MESSAGE", long + "\n" + long}, {"ADDON_ID", "x"}}, 3000},
		{"two long values", []Field{{"MESSAGE", long}, {"OTHER", long}}, 2000},
	} {
		got := Fit(c.fields, c.max)
		if n := len(Encode(got)); n > c.max {
			t.Errorf("%s: %d bytes, max %d", c.name, n, c.max)
		}
		if len(got) != len(c.fields) {
			t.Errorf("%s: fields %d, want %d", c.name, len(got), len(c.fields))
		}
		for i, f := range got {
			if f.Key != c.fields[i].Key || !utf8.ValidString(f.Value) {
				t.Errorf("%s: field %d is %q", c.name, i, f.Key)
			}
			if f.Value != c.fields[i].Value && !strings.HasSuffix(f.Value, " [truncated]") && f.Value != "" {
				t.Errorf("%s: %s cut without its mark", c.name, f.Key)
			}
		}
		// the room is used: not cut to nothing when a part fits
		if len(Encode(got)) < c.max/2 {
			t.Errorf("%s: cut to %d bytes of %d", c.name, len(Encode(got)), c.max)
		}
	}
	small := []Field{{"MESSAGE", "short"}}
	if got := Fit(small, 100); !reflect.DeepEqual(got, small) {
		t.Errorf("a fitting entry changed: %q", got)
	}
}

// listen is a fake journald: a unixgram socket in a temp dir, and what arrived at it.
func listen(t *testing.T) (string, func(n int) [][]Field) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "journal.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return sock, func(n int) [][]Field {
		t.Helper()
		var out [][]Field
		buf := make([]byte, 1<<20)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for len(out) < n {
			m, err := conn.Read(buf)
			if err != nil {
				t.Fatalf("after %d entries: %v", len(out), err)
			}
			f, err := Decode(buf[:m])
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, f)
		}
		return out
	}
}

func TestSend(t *testing.T) {
	sock, recv := listen(t)
	w := &Writer{Socket: sock}
	entries := [][]Field{
		{{"MESSAGE", "first"}, {"PRIORITY", "6"}, {"SYSLOG_IDENTIFIER", "addon-install"}},
		{{"MESSAGE", "a\nmulti-line\nvalue"}, {"PRIORITY", "3"}},
		{{"MESSAGE", strings.Repeat("x", 100000)}},
	}
	if err := w.Send(entries...); err != nil {
		t.Fatal(err)
	}
	got := recv(3)
	if !reflect.DeepEqual(got[:2], entries[:2]) {
		t.Errorf("got %q", got[:2])
	}
	// the third is larger than a datagram may be: cut to the limit, marked
	if m := got[2][0].Value; len(Encode(got[2])) > DefaultMaxDatagram || !strings.HasSuffix(m, " [truncated]") || !strings.HasPrefix(m, "xxxx") {
		t.Errorf("an oversized entry arrived as %d bytes", len(Encode(got[2])))
	}

	// a limit above what the socket takes: EMSGSIZE, and Send halves until it fits
	w = &Writer{Socket: sock, MaxDatagram: 64 << 20}
	if err := w.Send([]Field{{"MESSAGE", strings.Repeat("y", 8<<20)}}); err != nil {
		t.Fatal(err)
	}
	if m := recv(1)[0][0].Value; !strings.HasPrefix(m, "yyyy") || !strings.HasSuffix(m, " [truncated]") {
		t.Errorf("the halved entry: %d bytes", len(m))
	}
}

// No socket (a box without journald, a test root): an error at once, nothing else.
func TestSendWithoutSocket(t *testing.T) {
	start := time.Now()
	err := (&Writer{Socket: filepath.Join(t.TempDir(), "none")}).Send([]Field{{"MESSAGE", "x"}})
	if err == nil || time.Since(start) > time.Second {
		t.Errorf("%v after %v", err, time.Since(start))
	}
	if err := (&Writer{Socket: "/nonexistent"}).Send(); err != nil {
		t.Errorf("nothing to send: %v", err)
	}
	if !bytes.Equal(Encode(nil), nil) && len(Encode(nil)) != 0 {
		t.Error("an empty entry encodes to something")
	}
}
