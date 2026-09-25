package backuptarget

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSH targets use keys only (D-80): an ed25519 pair made on the system, the private half 0600 in
// the target's secret directory, the public half shown as an authorized_keys line. The server's
// key is pinned the first time the user confirms its fingerprint, and a different key later is
// the state host-key-changed, never accepted on its own.

// KeyComment is the public key's comment: which system and what for.
func KeyComment(hostname string) string {
	if hostname == "" {
		hostname = "openccu-lite"
	}
	return "openccu-lite-backup@" + hostname
}

// GenerateKey makes a new pair for the target (replacing an older one) and answers the public key
// line.
func GenerateKey(dir, hostname string) (string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, KeyComment(hostname))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(dir, KeyFile), pem.EncodeToMemory(block), 0o600); err != nil {
		return "", err
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " " + KeyComment(hostname)
	if err := writeAtomic(filepath.Join(dir, KeyFile+".pub"), []byte(line+"\n"), 0o644); err != nil {
		return "", err
	}
	return line, nil
}

func loadSigner(dir string) (ssh.Signer, error) {
	b, err := os.ReadFile(filepath.Join(dir, KeyFile))
	if err != nil {
		return nil, fmt.Errorf("no key for this target: %w", err)
	}
	return ssh.ParsePrivateKey(b)
}

// PublicKeyLine is the target's public key as an authorized_keys line (with the comment it was
// made with); ok false without a key.
func PublicKeyLine(dir string) (string, bool) {
	if line := readTrim(filepath.Join(dir, KeyFile+".pub")); line != "" {
		return line, true
	}
	s, err := loadSigner(dir)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey()))), true
}

// TrustedHostKey is the pinned server key, nil when none is.
func TrustedHostKey(dir string) *HostKeyInfo {
	k := trustedKey(dir)
	if k == nil {
		return nil
	}
	return &HostKeyInfo{Type: k.Type(), Fingerprint: ssh.FingerprintSHA256(k)}
}

func trustedKey(dir string) ssh.PublicKey {
	b, err := os.ReadFile(filepath.Join(dir, HostKey))
	if err != nil {
		return nil
	}
	k, _, _, _, err := ssh.ParseAuthorizedKey(b)
	if err != nil {
		return nil
	}
	return k
}

// TrustHostKey pins k.
func TrustHostKey(dir string, k ssh.PublicKey) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, HostKey), ssh.MarshalAuthorizedKey(k), 0o600)
}

// ForgetHostKey removes the pin (a changed server: the user compares and trusts again).
func ForgetHostKey(dir string) error {
	if err := os.Remove(filepath.Join(dir, HostKey)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// HostKeyError is a server key that is not the pinned one, or no pin at all.
type HostKeyError struct {
	Changed bool
	Key     ssh.PublicKey
}

func (e *HostKeyError) Error() string {
	if e.Changed {
		return "the server's host key changed: " + ssh.FingerprintSHA256(e.Key)
	}
	return "the server's host key is not trusted yet: " + ssh.FingerprintSHA256(e.Key)
}

func fingerprint(k ssh.PublicKey) string { return ssh.FingerprintSHA256(k) }

// errScanned ends a scan's handshake once the key is seen.
var errScanned = errors.New("host key scanned")

// hostKeyAlgorithms prefers the modern key types, and - once a key is pinned - asks for that
// type only, so a server that has several keys presents the pinned one.
func hostKeyAlgorithms(pinned ssh.PublicKey) []string {
	all := []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	if pinned == nil {
		return all
	}
	if pinned.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	return []string{pinned.Type()}
}

// DialTimeout bounds the TCP connect and the SSH handshake.
var DialTimeout = 15 * time.Second

// KeepaliveInterval is how often a connection asks whether the server is still there.
var KeepaliveInterval = 15 * time.Second

// dialSSH connects with the target's key and pin. ctx ends the connection too.
func dialSSH(ctx context.Context, s *SFTP, dir string, scan bool) (*ssh.Client, ssh.PublicKey, error) {
	var seen ssh.PublicKey
	pinned := trustedKey(dir)
	cfg := &ssh.ClientConfig{
		User:              s.User,
		Timeout:           DialTimeout,
		HostKeyAlgorithms: hostKeyAlgorithms(pinned),
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = key
			if scan {
				return errScanned
			}
			if pinned == nil {
				return &HostKeyError{Key: key}
			}
			if !bytes.Equal(pinned.Marshal(), key.Marshal()) {
				return &HostKeyError{Changed: true, Key: key}
			}
			return nil
		},
	}
	if scan {
		cfg.HostKeyAlgorithms = hostKeyAlgorithms(nil)
	} else {
		signer, err := loadSigner(dir)
		if err != nil {
			return nil, nil, err
		}
		cfg.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	d := net.Dialer{Timeout: DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(DialTimeout))
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		if scan && seen != nil {
			return nil, seen, nil
		}
		return nil, seen, err
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(c, chans, reqs)
	// a context that ends takes the connection with it: a hung server cannot hold a caller
	stop := context.AfterFunc(ctx, func() { client.Close() })
	done := make(chan struct{})
	go func() {
		_ = client.Wait()
		stop()
		close(done)
	}()
	// and a server that stops answering mid-transfer is noticed: a keepalive every 15 s, the
	// connection closed when one goes unanswered for 30 s
	go func() {
		tick := time.NewTicker(KeepaliveInterval)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
			}
			answered := make(chan struct{})
			go func() {
				_, _, _ = client.SendRequest("keepalive@openssh.com", true, nil)
				close(answered)
			}()
			select {
			case <-answered:
			case <-done:
				return
			case <-time.After(2 * KeepaliveInterval):
				client.Close()
				return
			}
		}
	}()
	return client, seen, nil
}

// ScanHostKey connects far enough to see the server's key.
func ScanHostKey(ctx context.Context, s *SFTP) (ssh.PublicKey, error) {
	_, k, err := dialSSH(ctx, s, "", true)
	if err != nil {
		return nil, err
	}
	if k == nil {
		return nil, errors.New("the server presented no host key")
	}
	return k, nil
}
