package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/firewall"
)

// Task 143 (D-95): System -> Remote access, classic CCU RPC. lighttpd is the door, as on a CCU:
// S50lighttpd runs lite-classic-rpc-conf at every start and reload, which writes the plain and TLS
// sockets from two userfs markers and adds basic auth when the pair's htpasswd file is there. The
// markers and the file *are* the settings, as with HTTPS (https.go): what the page shows is what
// lighttpd was last told. The interface processes stay on the loopback (D-29).
const (
	// ClassicRPCPlainMarker: 2001 BidCos-RF, 2010 HmIP-RF, 9292 VirtualDevices (2000 BidCos-Wired
	// where hs485d runs).
	ClassicRPCPlainMarker = "/etc/config/classicRpcPlain"
	// ClassicRPCTLSMarker: their TLS twins 42001, 42010, 49292 (42000), with the system's
	// certificate.
	ClassicRPCTLSMarker = "/etc/config/classicRpcTLS"
	// ClassicRPCHtpasswd is the pair, "<user>:<SHA-512-crypt>", root:www-data 0640 since B-120
	// (lighttpd runs as www-data; 0600 where the box has no such group). It exists only while
	// authentication is on.
	ClassicRPCHtpasswd = "/etc/config/classic-rpc.htpasswd"
	// HS485DEnabledMarker is the radio run's: hs485d runs on this system (BidCos-Wired).
	HS485DEnabledMarker = "/run/occulite/radio/hs485d.enabled"
	// AuthEnabledMarker is upstream's: OpenCCU asked the ReGa accounts for a login on its RPC
	// ports. lite has no ReGa; only the conversion reads it.
	AuthEnabledMarker = "/etc/config/authEnabled"

	// ClassicRPCMinPassword is a typed password's minimum, ClassicRPCGenerated a generated one's
	// length (D-73).
	ClassicRPCMinPassword = 12
	ClassicRPCGenerated   = 32
)

// ErrClassicRPCInvalid is a request the rules refuse; the message says which rule.
var ErrClassicRPCInvalid = errors.New("invalid classic RPC settings")

var classicUserRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// ClassicRPC is the classic part of GET/PUT /remote-access.
type ClassicRPC struct {
	Plain bool `json:"plain"`
	TLS   bool `json:"tls"`
	// Auth is "none" or "password" - the htpasswd file exists.
	Auth        string `json:"auth"`
	User        string `json:"user,omitempty"`
	PasswordSet bool   `json:"password_set"`
}

// ClassicRPCPort is one port a switch opens, with the interface behind it.
type ClassicRPCPort struct {
	Port      int    `json:"port"`
	TLS       bool   `json:"tls"`
	Interface string `json:"interface"` // BidCos-RF, HmIP-RF, VirtualDevices, BidCos-Wired
	Process   string `json:"process"`   // rfd, hmipserver, hs485d
	Backend   int    `json:"backend"`   // the process's loopback port lighttpd forwards to
}

// classicPorts are every classic port, plain first.
var classicPorts = []ClassicRPCPort{
	{2001, false, "BidCos-RF", "rfd", 32001},
	{2010, false, "HmIP-RF", "hmipserver", 32010},
	{9292, false, "VirtualDevices", "hmipserver", 39292},
	{2000, false, "BidCos-Wired", "hs485d", 32000},
	{42001, true, "BidCos-RF", "rfd", 32001},
	{42010, true, "HmIP-RF", "hmipserver", 32010},
	{49292, true, "VirtualDevices", "hmipserver", 39292},
	{42000, true, "BidCos-Wired", "hs485d", 32000},
}

// ReadClassicRPC reads the markers and the pair's user name.
func (r Root) ReadClassicRPC() ClassicRPC {
	c := ClassicRPC{Auth: "none"}
	if _, err := os.Stat(r.join(ClassicRPCPlainMarker)); err == nil {
		c.Plain = true
	}
	if _, err := os.Stat(r.join(ClassicRPCTLSMarker)); err == nil {
		c.TLS = true
	}
	// root 0600: the helper reads it for the user name; the hash stays inside occulited
	if b := readFile(r.join(ClassicRPCHtpasswd)); strings.TrimSpace(b) != "" {
		c.Auth, c.PasswordSet = "password", true
		c.User, _, _ = strings.Cut(strings.TrimSpace(b), ":")
	}
	return c
}

// HasHS485D says whether hs485d runs here, so 2000 and 42000 are served.
func (r Root) HasHS485D() bool {
	_, err := os.Stat(r.join(HS485DEnabledMarker))
	return err == nil
}

// ClassicRPCPorts are the ports the switches open: plain and TLS, 2000/42000 only where hs485d
// runs - what lite-classic-rpc-conf writes.
func ClassicRPCPorts(plain, tls, hs485d bool) []ClassicRPCPort {
	var out []ClassicRPCPort
	for _, p := range classicPorts {
		if p.Process == "hs485d" && !hs485d {
			continue
		}
		if (p.TLS && tls) || (!p.TLS && plain) {
			out = append(out, p)
		}
	}
	return out
}

// AllClassicRPCPorts is every port with its interface, for the page's lists.
func AllClassicRPCPorts(hs485d bool) []ClassicRPCPort { return ClassicRPCPorts(true, true, hs485d) }

// ClassicRPCOwner is the firewall owner's ports (task 157): a rule per open port, "local networks"
// by default (the maintainer, 2026-09-18: as OpenCCU ran *full* XMLRPC); nil while both are off.
func (r Root) ClassicRPCOwner() []firewall.PortSpec {
	c := r.ReadClassicRPC()
	return classicSpecs(ClassicRPCPorts(c.Plain, c.TLS, r.HasHS485D()))
}

func classicSpecs(ports []ClassicRPCPort) []firewall.PortSpec {
	var out []firewall.PortSpec
	for _, p := range ports {
		out = append(out, firewall.PortSpec{Port: p.Port, Proto: firewall.ProtoTCP, Comment: firewall.XMLRPCComments[p.Port]})
	}
	return out
}

// ClassicRPCConfig applies the settings: the markers and the pair through the privilege helper,
// then one lighttpd reload, then the firewall follows its owner.
type ClassicRPCConfig struct {
	Root    Root
	Run     Runner
	Systemd bool
	// RestartDelay is how long a switch change waits before it restarts lighttpd (default 1.5 s):
	// long enough for the answer to the page, which comes through lighttpd, to be out.
	RestartDelay time.Duration
	mu           sync.Mutex
}

// restartLighttpdSoon restarts lighttpd after RestartDelay, off the request. A reload is not
// enough when sockets come or go: lighttpd's graceful reload keeps a listening socket the new
// configuration no longer names, and it goes on answering with the global settings (found on the
// Charly, task 143). A restart runs the unit's ExecStartPre, which writes the sockets anew.
func (c *ClassicRPCConfig) restartLighttpdSoon(l certLog) {
	delay := c.RestartDelay
	if delay == 0 {
		delay = 1500 * time.Millisecond
	}
	runner := c.Run
	if runner == nil {
		runner = run
	}
	l.logf("lighttpd restarts in %s for the classic ports", delay)
	go func() {
		time.Sleep(delay)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var out []byte
		var err error
		if c.Systemd {
			out, err = runner(ctx, "systemctl", "restart", "--no-pager", "--", "lighttpd.service")
		} else {
			out, err = runner(ctx, c.Root.join("/etc/init.d/S50lighttpd"), "restart")
		}
		if err != nil {
			l.logf("the lighttpd restart failed: %v: %s", err, strings.TrimSpace(string(out)))
			return
		}
		l.logf("lighttpd restarted")
	}()
}

// Set switches plain and TLS and the authentication. auth "password" needs a pair already set
// (SetPassword); "none" removes it.
func (c *ClassicRPCConfig) Set(ctx context.Context, want ClassicRPC, log func(string)) (ClassicRPC, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.Root.ReadClassicRPC()
	switch want.Auth {
	case "none", "password":
	default:
		return cur, fmt.Errorf("%w: auth is none or password", ErrClassicRPCInvalid)
	}
	if want.Auth == "password" && !cur.PasswordSet {
		return cur, fmt.Errorf("%w: set a user name and password first", ErrClassicRPCInvalid)
	}
	if want.Plain == cur.Plain && want.TLS == cur.TLS && want.Auth == cur.Auth {
		return cur, nil
	}
	for _, m := range []struct {
		path string
		on   bool
	}{{ClassicRPCPlainMarker, want.Plain}, {ClassicRPCTLSMarker, want.TLS}} {
		var err error
		if m.on {
			err = writeFileAtomic(c.Root.join(m.path), nil, 0o644)
		} else {
			err = Priv.Remove(c.Root.join(m.path))
		}
		if err != nil {
			return cur, fmt.Errorf("%s: %w", m.path, err)
		}
	}
	if want.Auth == "none" && cur.PasswordSet {
		if err := Priv.Remove(c.Root.join(ClassicRPCHtpasswd)); err != nil {
			return cur, fmt.Errorf("%s: %w", ClassicRPCHtpasswd, err)
		}
	}
	l := certLog(log)
	l.logf("classic RPC: plain %s, TLS %s, authentication %s", onOff(want.Plain), onOff(want.TLS), want.Auth)
	var err error
	if want.Plain != cur.Plain || want.TLS != cur.TLS {
		c.restartLighttpdSoon(l)
	} else {
		// only the authentication: the same sockets, a reload does
		err = reloadLighttpd(ctx, c.Root, c.Run, c.Systemd, l)
	}
	notifyFirewall(ctx)
	return c.Root.ReadClassicRPC(), err
}

// SetPassword sets the pair: a typed password (at least ClassicRPCMinPassword characters) or a
// generated one, answered once and never again. The plaintext is not kept or logged.
func (c *ClassicRPCConfig) SetPassword(ctx context.Context, user, password string, generate bool, log func(string)) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !classicUserRe.MatchString(user) {
		return "", fmt.Errorf("%w: the user name has 1 to 32 letters, digits, dots, underscores or hyphens", ErrClassicRPCInvalid)
	}
	if generate {
		password = randomFrom("ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789", ClassicRPCGenerated)
	} else if len([]rune(password)) < ClassicRPCMinPassword || len(password) > 256 || strings.ContainsAny(password, "\r\n") {
		return "", fmt.Errorf("%w: the password has at least %d characters, on one line", ErrClassicRPCInvalid, ClassicRPCMinPassword)
	}
	line := user + ":" + sha512Crypt(password, newCryptSalt()) + "\n"
	if err := writeFileAtomic(c.Root.join(ClassicRPCHtpasswd), []byte(line), 0o600); err != nil {
		return "", fmt.Errorf("%s: %w", ClassicRPCHtpasswd, err)
	}
	if err := groupReadable(c.Root, c.Root.join(ClassicRPCHtpasswd), "www-data"); err != nil {
		// the pair is in place; lighttpd cannot read it until the next start's lite-classic-rpc-conf
		// repairs the mode (a development box without the helper lands here)
		certLog(log).logf("classic RPC: the pair could not be handed to lighttpd's group: %v", err)
	}
	l := certLog(log)
	how := "typed"
	if generate {
		how = "generated"
	}
	l.logf("classic RPC: the user name and password set for %q (%s password)", user, how)
	// the reload clears lighttpd's auth cache, so an old password stops working at once
	if err := reloadLighttpd(ctx, c.Root, c.Run, c.Systemd, l); err != nil {
		return "", err
	}
	if generate {
		return password, nil
	}
	return "", nil
}

// groupReadable hands a root 0600 file to a group as 0640 - the classic RPC pair to lighttpd's
// user (B-120). A box without the group (the busybox products, where lighttpd is root) keeps the
// file as it is. The group is the system's (r), not the host's the process happens to run on (B-7).
func groupReadable(r Root, path, group string) error {
	gid, ok := r.GroupID(group)
	if !ok {
		return nil
	}
	if err := Priv.Chown(path, 0, gid, false); err != nil {
		return err
	}
	return Priv.Chmod(path, 0o640)
}
