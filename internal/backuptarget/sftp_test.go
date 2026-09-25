package backuptarget

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// An in-process SSH server with pkg/sftp's server behind it, rooted in a temporary directory: the
// SFTP target's paths against real protocol answers - refused keys, a changed host key, no SFTP
// subsystem, a read-only server.

type sftpServer struct {
	addr     string
	root     string
	hostKey  ssh.Signer
	mu       sync.Mutex
	allowed  []ssh.PublicKey
	noSFTP   bool
	readOnly bool
}

func newSFTPServer(t *testing.T) *sftpServer {
	t.Helper()
	_, hk, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(hk)
	if err != nil {
		t.Fatal(err)
	}
	s := &sftpServer{root: t.TempDir(), hostKey: signer}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s.addr = l.Addr().String()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

// set changes the server's settings while connections may be served: under its mutex, like the
// reads in serve (the race detector saw the test swap the host key under a running connection).
func (s *sftpServer) set(fn func(*sftpServer)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func (s *sftpServer) settings() (hostKey ssh.Signer, noSFTP, readOnly bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostKey, s.noSFTP, s.readOnly
}

func (s *sftpServer) allow(line string) {
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		panic(err)
	}
	s.mu.Lock()
	s.allowed = append(s.allowed, k)
	s.mu.Unlock()
}

func (s *sftpServer) serve(c net.Conn) {
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, k := range s.allowed {
			if string(k.Marshal()) == string(key.Marshal()) {
				return nil, nil
			}
		}
		return nil, errors.New("not allowed")
	}}
	hostKey, noSFTP, readOnly := s.settings()
	cfg.AddHostKey(hostKey)
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		ch, in, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			for req := range in {
				ok := req.Type == "subsystem" && string(req.Payload[4:]) == "sftp" && !noSFTP
				_ = req.Reply(ok, nil)
				if !ok {
					continue
				}
				opts := []sftp.ServerOption{sftp.WithServerWorkingDirectory(s.root)}
				if readOnly {
					opts = append(opts, sftp.ReadOnly())
				}
				srv, err := sftp.NewServer(ch, opts...)
				if err != nil {
					ch.Close()
					return
				}
				_ = srv.Serve()
				ch.Close()
				return
			}
		}()
	}
}

func (s *sftpServer) target(t *testing.T, st *Store) Target {
	t.Helper()
	host, port, _ := net.SplitHostPort(s.addr)
	p, _ := strconv.Atoi(port)
	tg, err := st.Put(Target{Name: "lab", Kind: KindSFTP, Enabled: true, MaxBackups: 2, Subdir: "box", Encrypt: true,
		SFTP: &SFTP{Host: host, Port: p, User: "backup", Path: "backups"}})
	if err != nil {
		t.Fatal(err)
	}
	line, err := GenerateKey(st.SecretDir(tg.ID), "lab-box")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(line, " openccu-lite-backup@lab-box") {
		t.Fatal(line)
	}
	s.allow(line)
	return tg
}

func TestSFTPHostKeyAndAuth(t *testing.T) {
	srv := newSFTPServer(t)
	st := &Store{Dir: t.TempDir(), Root: t.TempDir()}
	tg := srv.target(t, st)
	dir := st.SecretDir(tg.ID)
	ctx := context.Background()
	// no pin yet: host-key-unknown
	_, err := DialSFTP(ctx, tg.SFTP, dir)
	if Classify(err) != StateHostKeyUnknown {
		t.Fatalf("unpinned: %v (%s)", err, Classify(err))
	}
	k, err := ScanHostKey(ctx, tg.SFTP)
	if err != nil || fingerprint(k) != ssh.FingerprintSHA256(srv.hostKey.PublicKey()) {
		t.Fatalf("scan: %v", err)
	}
	if err := TrustHostKey(dir, k); err != nil {
		t.Fatal(err)
	}
	if hk := TrustedHostKey(dir); hk == nil || hk.Type != "ssh-ed25519" {
		t.Fatalf("%+v", hk)
	}
	c, err := DialSFTP(ctx, tg.SFTP, dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	// another key on the server: host-key-changed, never accepted
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(other)
	srv.set(func(s *sftpServer) { s.hostKey = otherSigner })
	if _, err := DialSFTP(ctx, tg.SFTP, dir); Classify(err) != StateHostKeyChanged {
		t.Fatalf("changed: %v (%s)", err, Classify(err))
	}
	// trusted again, then a new key on our side the server does not know: auth-failed
	k, _ = ScanHostKey(ctx, tg.SFTP)
	_ = TrustHostKey(dir, k)
	if _, err := GenerateKey(dir, "lab-box"); err != nil {
		t.Fatal(err)
	}
	if _, err := DialSFTP(ctx, tg.SFTP, dir); Classify(err) != StateAuthFailed {
		t.Fatalf("refused key: %v (%s)", err, Classify(err))
	}
	// nobody listening: unreachable
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	dead := *tg.SFTP
	dead.Port, _ = strconv.Atoi(port)
	if _, err := DialSFTP(ctx, &dead, dir); Classify(err) != StateUnreachable {
		t.Fatalf("dead: %v (%s)", err, Classify(err))
	}
}

func trusted(t *testing.T, srv *sftpServer, st *Store) Target {
	t.Helper()
	tg := srv.target(t, st)
	k, err := ScanHostKey(context.Background(), tg.SFTP)
	if err != nil {
		t.Fatal(err)
	}
	if err := TrustHostKey(st.SecretDir(tg.ID), k); err != nil {
		t.Fatal(err)
	}
	return tg
}

func TestSFTPUploadListRetention(t *testing.T) {
	srv := newSFTPServer(t)
	st := &Store{Dir: t.TempDir(), Root: t.TempDir()}
	tg := trusted(t, srv, st)
	ctx := context.Background()
	c, err := DialSFTP(ctx, tg.SFTP, st.SecretDir(tg.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := c.Test(tg.RemoteDir())
	if !r.OK || r.State != StateWritable || r.Step != "done" {
		t.Fatalf("%+v", r)
	}
	left, _ := os.ReadDir(filepath.Join(srv.root, "backups", "box"))
	if len(left) != 0 {
		t.Fatalf("the test left %v", left)
	}
	for i, n := range []string{"lab-box-1-2026-09-01-0007.sbk.age", "lab-box-1-2026-09-02-0007.sbk.age", "other-1-2026-09-01-0007.sbk"} {
		src := strings.NewReader(strings.Repeat("x", 100+i))
		if err := c.Upload(tg.RemoteDir(), n, src, int64(100+i)); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(srv.root, "backups", "box", n)
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", n, err, st.Mode())
		}
		when := time.Date(2026, 9, 1+i, 0, 7, 0, 0, time.UTC)
		_ = os.Chtimes(p, when, when)
	}
	// a short write is refused and leaves no .partial
	if err := c.Upload(tg.RemoteDir(), "lab-box-x.sbk", strings.NewReader("abc"), 10); err == nil {
		t.Fatal("short upload accepted")
	}
	if _, err := os.Stat(filepath.Join(srv.root, "backups", "box", "lab-box-x.sbk.partial")); !os.IsNotExist(err) {
		t.Fatalf("partial left: %v", err)
	}
	files, err := c.List(tg.RemoteDir())
	if err != nil || len(files) != 3 || files[0].Name != "other-1-2026-09-01-0007.sbk" {
		t.Fatalf("%v %+v", err, files)
	}
	exp := Expired(files, "lab-box", 1)
	if len(exp) != 1 || exp[0] != "lab-box-1-2026-09-01-0007.sbk.age" {
		t.Fatalf("expired %v", exp)
	}
}

func TestSFTPNoSubsystemReadOnly(t *testing.T) {
	srv := newSFTPServer(t)
	st := &Store{Dir: t.TempDir(), Root: t.TempDir()}
	tg := trusted(t, srv, st)
	ctx := context.Background()
	srv.set(func(s *sftpServer) { s.noSFTP = true })
	if _, err := DialSFTP(ctx, tg.SFTP, st.SecretDir(tg.ID)); Classify(err) != StateNoSFTP {
		t.Fatalf("no sftp: %v (%s)", err, Classify(err))
	}
	srv.set(func(s *sftpServer) { s.noSFTP, s.readOnly = false, true })
	c, err := DialSFTP(ctx, tg.SFTP, st.SecretDir(tg.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := c.Test(tg.RemoteDir())
	if r.OK || r.State != StateReadOnly {
		t.Fatalf("%+v", r)
	}
}
