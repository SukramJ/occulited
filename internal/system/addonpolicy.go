package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// Per-addon users and confinement (D-36, task 18) on the systemd products. The state of an
// addon is one JSON file on the userfs, /usr/local/etc/config/addon-policy/<id>.json, and next
// to it the systemd drop-in occulited renders from it, <id>.conf; the occu-addons generator
// installs that drop-in as addon-<id>.service.d/10-policy.conf, and occu-addons.service
// recreates the users from the same files at boot (the rootfs, and with it /etc/passwd, is
// replaced by every firmware update; the uid in the file keeps the ownership on the userfs
// valid). The busybox products ignore all of it.

// AddonPolicyDir is where the files live.
const AddonPolicyDir = "/usr/local/etc/config/addon-policy"

// AddonUIDBase is the first uid handed to an addon; the CCU's own users stay below.
const AddonUIDBase = 30000

// AddonRuntime is the manifest's runtime block as the policy stores it (docs/manifest-format.md,
// D-119; until 2026-09-23 the catalogue entry's).
//
// Ports is a plain list of numbers; what a port is - its protocol, whether it speaks TLS, a
// label - lives in the sibling map PortInfo, keyed by the port as a string. DeclaredPorts joins
// the two.
type AddonRuntime struct {
	Root         bool     `json:"root,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Groups       []string `json:"groups,omitempty"`
	Paths        []string `json:"paths,omitempty"`
	// DataDirs are the addon's own state directories outside its three standard ones (D-52):
	// taken over - chowned to the addon's user and put on ReadWritePaths - when the addon is
	// confined. Paths is not that: it may name a shared directory (rc.d) and is never chowned.
	DataDirs []string            `json:"data_dirs,omitempty"`
	Ports    []int               `json:"ports,omitempty"`
	PortInfo map[string]PortInfo `json:"port_info,omitempty"`
	// Needs are the interface processes the addon talks to (task 94), ids from AddonNeedsIDs: the
	// occu-addons generator orders its unit after them. nil = undeclared, the safe default (after
	// rfd and hmipserver, as before); an empty list = it needs none and starts right after the
	// network. A pointer, so that the two stay apart in the policy file as on the wire.
	Needs *[]string `json:"needs,omitempty"`
	// Start is the manifest's runtime.start (task 119, D-75): AddonStartEarly, or "" for the
	// default ordering. occulited writes <id>.start from it (addonstart.go) unless the user
	// switched the early start off.
	Start string `json:"start,omitempty"`
	// Daemon is the manifest's runtime.daemon (openccu-lite B-158): the addon keeps a process
	// running, so its unit ending up empty means its daemon ended (addondaemon.go).
	Daemon bool `json:"daemon,omitempty"`
	// Session is the old catalogue's runtime.session (task 88, D-67): from which version on the
	// addon reads the gate's X-Occulite-Session. Written by no install since D-119 - the manifest's
	// ui.session_header is per version - and read only for a policy without a stored manifest
	// (httpapi.SessionHeader).
	Session *AddonSession `json:"session,omitempty"`
	// SettingsURL is the old catalogue's runtime.settings_url (B-134), the same way: the manifest's
	// ui.settings_url wins where a manifest is stored (httpapi.SettingsURL).
	SettingsURL string `json:"settings_url,omitempty"`
	// APIScopes is the manifest's runtime.api_scopes (task 66, D-85): the scopes the addon's own
	// API token carries, minted at every start (AddonTokens). What the auth store refuses an addon
	// - Full access, auth:admin, power, backup, a name it does not know - is left out there and
	// logged. nil = no declaration, the addon has the box's local token alone.
	APIScopes []string `json:"api_scopes,omitempty"`
}

// CapSysAdmin is the capability that mounts (and much else); no root addon has it unless its
// entry declares it (D-66).
const CapSysAdmin = "CAP_SYS_ADMIN"

// DeclaresCapability says whether the runtime block names the capability.
func (rt *AddonRuntime) DeclaresCapability(name string) bool {
	return rt != nil && slices.Contains(rt.Capabilities, name)
}

// AddonSession is runtime.session as the policy stores it.
type AddonSession struct {
	HeaderSince string `json:"header_since,omitempty"`
}

// PortInfo describes one declared port. Proto is informational: libfirewall's USERPORTS opens
// TCP and UDP both per number, and the page says so. TLS shows a badge, so a user can tell the
// TLS listener from the plaintext one and open only that (D-47). Label is de/en.
type PortInfo struct {
	Proto string            `json:"proto,omitempty"`
	TLS   bool              `json:"tls,omitempty"`
	Label map[string]string `json:"label,omitempty"`
}

// Port is one declared port with its description, as the Firewall page lists it.
type Port struct {
	Port  int               `json:"port"`
	Proto string            `json:"proto,omitempty"`
	TLS   bool              `json:"tls,omitempty"`
	Label map[string]string `json:"label,omitempty"`
}

// DeclaredPorts is the runtime block's ports in declared order, each once, with what PortInfo
// says about it; a port without an entry there has no label and tls false. Nil for no block.
func (rt *AddonRuntime) DeclaredPorts() []Port {
	if rt == nil {
		return nil
	}
	var out []Port
	seen := map[int]bool{}
	for _, n := range rt.Ports {
		if n < 1 || n > 65535 || seen[n] {
			continue
		}
		seen[n] = true
		p := Port{Port: n}
		if info, ok := rt.PortInfo[strconv.Itoa(n)]; ok {
			p.Proto, p.TLS, p.Label = info.Proto, info.TLS, info.Label
		}
		out = append(out, p)
	}
	return out
}

// Declares says whether the runtime block names the port.
func (rt *AddonRuntime) Declares(port int) bool {
	return rt != nil && slices.Contains(rt.Ports, port)
}

// MergeRuntime is what an addon declares today: the stored policy's block joined with its stored
// manifest's (union by port, the manifest's port_info winning). Since D-119 the two are written
// from the same file and differ only for a policy written before the manifest was stored, or by a
// binary that did not know a key; an addon without a manifest keeps the policy's block alone. Nil
// when neither says anything.
func MergeRuntime(policy, cat *AddonRuntime) *AddonRuntime {
	if cat == nil {
		return policy
	}
	if policy == nil {
		return cat
	}
	out := &AddonRuntime{Root: policy.Root, Capabilities: policy.Capabilities, Groups: policy.Groups, Paths: policy.Paths, DataDirs: policy.DataDirs, Needs: policy.Needs, Start: policy.Start, Daemon: policy.Daemon || cat.Daemon, Session: policy.Session, SettingsURL: policy.SettingsURL, APIScopes: policy.APIScopes}
	// task 94: what the addon talks to is a fact about the addon, not a grant anybody decided -
	// the manifest's word wins when it says anything, whatever the policy's source
	if cat.Needs != nil {
		out.Needs = cat.Needs
	}
	// the same for whether it copes with an early start (task 119)
	if cat.Start != "" {
		out.Start = cat.Start
	}
	// the same for how it learns the session and where its settings page is (task 88, B-134)
	if cat.Session != nil {
		out.Session = cat.Session
	}
	if cat.SettingsURL != "" {
		out.SettingsURL = cat.SettingsURL
	}
	// and for the scopes of the addon's API token (task 66): a declaration, not a grant anybody
	// decided on the box
	if cat.APIScopes != nil {
		out.APIScopes = cat.APIScopes
	}
	out.Ports = append(append([]int(nil), policy.Ports...), cat.Ports...)
	slices.Sort(out.Ports)
	out.Ports = slices.Compact(out.Ports)
	for k, v := range policy.PortInfo {
		if out.PortInfo == nil {
			out.PortInfo = map[string]PortInfo{}
		}
		out.PortInfo[k] = v
	}
	for k, v := range cat.PortInfo {
		if out.PortInfo == nil {
			out.PortInfo = map[string]PortInfo{}
		}
		out.PortInfo[k] = v
	}
	return out
}

// AddonPolicy is how one addon runs.
type AddonPolicy struct {
	ID string `json:"id"`
	// Mode is "root" (the unit runs as root, as the rc.d ABI always did) or "confined" (its own
	// user with the grants below).
	Mode    string        `json:"mode"`
	UID     int           `json:"uid,omitempty"`
	User    string        `json:"user,omitempty"`
	Runtime *AddonRuntime `json:"runtime,omitempty"`
	// Source says where the mode came from: "default", "manifest", "catalog", "user", "migrated"
	// or "fallback" (AddonPolicyView.Source).
	Source string `json:"source,omitempty"`
	// DataDirs are the data directories the take-over (D-52) found for the addon and chowned
	// the last time the policy was written or refreshed: the runtime block's data_dirs plus
	// the convention /usr/local/<id>, after the guard rails. The drop-in is rendered from this
	// list, so what the unit may write is what the addon owns.
	DataDirs []string `json:"data_dirs,omitempty"`
	// OpenPorts are the declared ports the user opened on the Firewall page (D-47): the firewall's
	// USERPORTS is the user's own list plus these, for every installed addon. None by default
	// (D-29), whatever the mode - the declaration is about reachability, not confinement.
	OpenPorts []int `json:"open_ports,omitempty"`
}

var (
	addonIDRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}$`)
	capRe     = regexp.MustCompile(`^CAP_[A-Z_]{1,40}$`)
	groupRe   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	pathRe    = regexp.MustCompile(`^/[A-Za-z0-9_./-]{0,200}$`)
	// scopeNameRe is the shape of a scope name; which names exist the auth store decides
	scopeNameRe = regexp.MustCompile(`^(\*|[a-z]+(:[a-z]+)?)$`)
)

// ReadAddonPolicy returns the stored policy, nil when the addon has none.
func (r Root) ReadAddonPolicy(id string) *AddonPolicy {
	if !addonIDRe.MatchString(id) {
		return nil
	}
	b, err := os.ReadFile(r.join(AddonPolicyDir + "/" + id + ".json"))
	if err != nil {
		return nil
	}
	var p AddonPolicy
	if json.Unmarshal(b, &p) != nil || p.ID != id {
		return nil
	}
	return &p
}

// AddonPolicies lists every stored policy.
func (r Root) AddonPolicies() map[string]*AddonPolicy {
	out := map[string]*AddonPolicy{}
	entries, _ := os.ReadDir(r.join(AddonPolicyDir))
	for _, e := range entries {
		if id := strings.TrimSuffix(e.Name(), ".json"); id != e.Name() {
			if p := r.ReadAddonPolicy(id); p != nil {
				out[id] = p
			}
		}
	}
	return out
}

func (rt *AddonRuntime) validate() error {
	if rt == nil {
		return nil
	}
	for _, c := range rt.Capabilities {
		if !capRe.MatchString(c) {
			return fmt.Errorf("capability %q", c)
		}
	}
	for _, g := range rt.Groups {
		if !groupRe.MatchString(g) {
			return fmt.Errorf("group %q", g)
		}
	}
	for _, p := range rt.Paths {
		if !pathRe.MatchString(p) || strings.Contains(p, "..") {
			return fmt.Errorf("path %q", p)
		}
	}
	for _, d := range rt.DataDirs {
		if err := dataDirShape(d); err != nil {
			return fmt.Errorf("data_dir %q: %w", d, err)
		}
	}
	for _, n := range rt.Ports {
		if n < 1 || n > 65535 {
			return fmt.Errorf("port %d", n)
		}
	}
	for _, s := range rt.APIScopes {
		if !scopeNameRe.MatchString(s) {
			return fmt.Errorf("api_scope %q", s)
		}
	}
	for k := range rt.PortInfo {
		if n, err := strconv.Atoi(k); err != nil || !slices.Contains(rt.Ports, n) {
			return fmt.Errorf("port_info %q names no declared port", k)
		}
	}
	return nil
}

// pruneOpenPorts drops opened ports the declaration no longer names - a catalogue update that
// took a port away closes it, the user cannot keep open what nobody declares.
func (p *AddonPolicy) pruneOpenPorts(declared *AddonRuntime) {
	var keep []int
	for _, n := range p.OpenPorts {
		if declared.Declares(n) && !slices.Contains(keep, n) {
			keep = append(keep, n)
		}
	}
	p.OpenPorts = keep
}

// SetAddonOpenPorts stores which of an addon's declared ports are open (D-47) and returns the
// policy. Every port must be in declared - the merged declaration (MergeRuntime) the caller
// holds; nil means the policy's own block - and an addon without a policy has nothing to open.
// The firewall file is the caller's business (FirewallManager.SetAddonPorts does both). The
// drop-in is untouched: ports are not confinement.
func (r Root) SetAddonOpenPorts(id string, open []int, declared *AddonRuntime) (*AddonPolicy, error) {
	policyMu.Lock()
	defer policyMu.Unlock()
	p := r.ReadAddonPolicy(id)
	if p == nil {
		return nil, fmt.Errorf("addon %q has no policy", id)
	}
	if declared == nil {
		declared = p.Runtime
	}
	var ports []int
	for _, n := range open {
		if !declared.Declares(n) {
			return nil, fmt.Errorf("port %d is not declared by %s", n, id)
		}
		if !slices.Contains(ports, n) {
			ports = append(ports, n)
		}
	}
	sort.Ints(ports)
	p.OpenPorts = ports
	return p, r.writeAddonPolicy(p)
}

// writeAddonPolicy writes the policy's JSON (not the drop-in).
func (r Root) writeAddonPolicy(p *AddonPolicy) error {
	dir := r.join(AddonPolicyDir)
	if err := Priv.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	return writeFileAtomic(filepath.Join(dir, p.ID+".json"), append(b, '\n'), 0o644)
}

// renderDropIn is the systemd fragment for a policy. Root mode renders a comment only: the
// generator installs a drop-in only when it has a [Service] section.
// HasGroup says whether /etc/group names the group - the image's table, plus what
// occu-etc-writable let addon users add.
func (r Root) HasGroup(name string) bool {
	for _, line := range strings.Split(readFile(r.join("/etc/group")), "\n") {
		if n, _, ok := strings.Cut(line, ":"); ok && n == name {
			return true
		}
	}
	return false
}

// CertsGroup is the group that may read the box's TLS certificate and key (D-46): the image
// makes /etc/config/server.pem root:certs 0640 (lite-cert-perms after every lighttpd start and
// reload), and every confined addon joins it, so a broker or a web server behind its own user
// can offer TLS with the same certificate lighttpd does. Only when the group exists on the box:
// systemd refuses a unit whose supplementary group is unknown, and an occulited newer than its
// image must not fail every confined addon.
const CertsGroup = "certs"

func renderDropIn(p *AddonPolicy, certsGroup bool) string {
	var b strings.Builder
	// the header is part of what the unit editor shows as the effective unit, so it names no
	// decision id (task 52)
	fmt.Fprintf(&b, "# openccu-lite addon policy for %s: written by occulited, installed by the\n# occu-addons generator as addon-%s.service.d/10-policy.conf. Change it on the Services page.\n", p.ID, p.ID)
	rt := p.Runtime
	if rt == nil {
		rt = &AddonRuntime{}
	}
	if p.Mode != "confined" {
		b.WriteString("# mode=root\n")
		// D-66: a root addon keeps root, not the right to mount. Without CAP_SYS_ADMIN the
		// CCU-era `mount -o remount,rw /` around an addon script's writes answers "permission
		// denied" and the script goes on; what it writes into /firmware/rftypes lands in the
		// writable layer the image mounts there (occu-extension-dirs). An entry that truly needs
		// mounting declares CAP_SYS_ADMIN and keeps the full set - the generator installs a
		// drop-in only when the file has a [Service] section, so that case writes none.
		if rt.DeclaresCapability(CapSysAdmin) {
			b.WriteString("# may mount: the addon's entry declares CAP_SYS_ADMIN\n")
			return b.String()
		}
		b.WriteString("[Service]\nCapabilityBoundingSet=~" + CapSysAdmin + "\n")
		return b.String()
	}
	fmt.Fprintf(&b, "# mode=confined uid=%d\n[Service]\nUser=%s\nGroup=%s\n", p.UID, p.User, p.User)
	groups := append([]string(nil), rt.Groups...)
	if certsGroup && !slices.Contains(groups, CertsGroup) {
		groups = append(groups, CertsGroup)
	}
	if len(groups) > 0 {
		fmt.Fprintf(&b, "SupplementaryGroups=%s\n", strings.Join(groups, " "))
	}
	if len(rt.Capabilities) > 0 {
		caps := strings.Join(rt.Capabilities, " ")
		fmt.Fprintf(&b, "AmbientCapabilities=%s\nCapabilityBoundingSet=%s\n", caps, caps)
	} else {
		b.WriteString("CapabilityBoundingSet=\n")
	}
	// "-": a path that does not exist is skipped instead of failing the unit (226/NAMESPACE)
	paths := []string{"/usr/local/addons/" + p.ID, "/usr/local/etc/config/addons/" + p.ID, "/usr/local/etc/config/rc.d", "/run", "/var/log", "/tmp", "/var/tmp"}
	paths = append(paths, p.DataDirs...) // D-52: what the take-over chowned, and nothing it refused
	paths = append(paths, rt.Paths...)
	for i := range paths {
		paths[i] = "-" + paths[i]
	}
	b.WriteString("NoNewPrivileges=yes\nProtectSystem=strict\nProtectKernelTunables=yes\nProtectControlGroups=yes\nRestrictSUIDSGID=yes\n")
	// /run/addon-<id>, owned by the user: where a confined addon can keep its pid file
	// (/var/run/<name>.pid is root's; the addon decides whether to use it)
	fmt.Fprintf(&b, "RuntimeDirectory=addon-%s\nRuntimeDirectoryPreserve=yes\n", p.ID)
	fmt.Fprintf(&b, "ReadWritePaths=%s\n", strings.Join(paths, " "))
	return b.String()
}

// checkPolicy refuses an id or a mode SetPolicy would refuse, before anything is done.
func checkPolicy(id, mode string) error {
	if !addonIDRe.MatchString(id) {
		return errors.New("invalid addon id")
	}
	if mode != "root" && mode != "confined" {
		return errors.New("mode must be root or confined")
	}
	return nil
}

// PolicySwitch is what SwitchPolicy did.
type PolicySwitch struct {
	Policy *AddonPolicy
	// Restarted: the unit was started again; RestartError says why it was not
	Restarted    bool
	RestartError string
}

// SwitchPolicy is the Services page's switch between root and the addon's own user, with its
// restart (B-106). The addon is stopped first - its unit until nothing of its cgroup is left, and a
// daemon of it that runs outside the unit - so that a root daemon's last writes (Mosquitto saves its
// persistence when it is stopped) are done before SetPolicy gives the addon's files to its user;
// then the unit is started, as the new user. A SetPolicy that fails after the stop still starts the
// unit again, in the mode the addon had, and answers the error.
func (a *SystemdAddons) SwitchPolicy(ctx context.Context, id, mode, source string) (PolicySwitch, error) {
	if err := checkPolicy(id, mode); err != nil {
		return PolicySwitch{}, err
	}
	// a daemon outside its unit while the unit has nothing of its own (task 48's stray)
	strays := a.Systemd.addonStray(ctx, id)
	_, qerr := a.Systemd.quietAddon(ctx, id, func() []proc { return strays })
	p, err := a.SetPolicy(ctx, id, mode, source, nil)
	sw := PolicySwitch{Policy: p}
	if qerr != nil {
		sw.RestartError = qerr.Error()
		return sw, err
	}
	if serr := a.Systemd.startAddonUnit(ctx, id); serr != nil {
		sw.RestartError = serr.Error()
		return sw, err
	}
	sw.Restarted = true
	if err == nil && a.confinedPolicy(id) != nil && a.AfterStart != nil {
		a.AfterStart([]string{id})
	}
	return sw, err
}

// SetPolicy stores how an addon runs and, for "confined", makes sure its user exists and owns
// the addon's directories. The unit picks the change up at the next daemon-reload (done here)
// and restart (the caller's decision: SwitchPolicy stops the addon before and starts it after).
// rt nil keeps the runtime block already stored.
func (a *SystemdAddons) SetPolicy(ctx context.Context, id, mode, source string, rt *AddonRuntime) (*AddonPolicy, error) {
	if err := checkPolicy(id, mode); err != nil {
		return nil, err
	}
	if err := rt.validate(); err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	policyMu.Lock()
	defer policyMu.Unlock()
	root := a.Scripts.Root
	p := root.ReadAddonPolicy(id)
	if p == nil {
		p = &AddonPolicy{ID: id}
	}
	wasConfined := p.Mode == "confined" && p.UID > 0
	if rt != nil {
		p.Runtime = rt
		p.pruneOpenPorts(MergeRuntime(rt, a.declared(id)))
	}
	p.Mode, p.Source = mode, source
	if mode == "confined" {
		if p.UID == 0 {
			p.UID = a.nextUID()
		}
		p.User = "addon-" + id
		if err := a.ensureUser(p); err != nil {
			return nil, err
		}
		// task 110: what the addon wrote as root until now is root's anywhere in its tree, and a root
		// daemon still running may write more: the next start walks the whole of it
		if !wasConfined {
			a.markFullWalk("policy", id)
		}
		// the config directory exists for almost no addon; create it so the addon can use it
		_ = Priv.MkdirAll(root.join("/usr/local/etc/config/addons/"+id), 0o755)
		if err := a.ownAddonDirs(ctx, p); err != nil {
			return nil, err
		}
	}
	if err := root.writeAddonPolicy(p); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(root.join(AddonPolicyDir), id+".conf"), []byte(renderDropIn(p, root.HasGroup(CertsGroup))), 0o644); err != nil {
		return nil, err
	}
	// task 94: the start order follows the declaration - the stored manifest, else the stored
	// block - in either mode; the generator reads it at the next boot
	if _, err := root.writeAddonNeeds(id, MergeRuntime(p.Runtime, a.declared(id))); err != nil {
		return nil, err
	}
	// task 119: and so does the early start, unless the user switched it off
	if _, err := a.writeAddonStart(id, MergeRuntime(p.Runtime, a.declared(id))); err != nil {
		return nil, err
	}
	_, _ = a.Systemd.run(ctx, "daemon-reload")
	a.RefreshAddonTokens(ctx) // 28.8: the token file follows the addon's user
	a.regenerateFirewall(ctx) // D-47: a changed runtime block can take a declared port away
	return p, nil
}

// declared is the addon's stored manifest's runtime block, nil without one (D-119).
func (a *SystemdAddons) declared(id string) *AddonRuntime {
	return a.Scripts.Root.DeclaredRuntime(id)
}

// ownAddonDirs gives a confined addon's user what it runs from: the three standard directories
// (the helper's owntree, task 107: no link followed, nothing on another file system, only entries
// with another owner changed; a missing one is skipped) and its data directories (D-52's take-over,
// which records the list it chowned on p without writing the policy). SetPolicy runs it when it
// confines an addon, Install again for an updated confined addon before the restart - what an
// update script wrote as root would otherwise stay root's, and the start as addon-<id> fails on it
// (B-92). The first step that fails ends it with that error.
//
// Until task 107 the three were `chown -R` through the helper's program list: busybox's recursion
// opens every directory by its path again after its lstat, so a directory the addon's user swapped
// for a link in between was entered, and root gave away whatever the link led to.
func (a *SystemdAddons) ownAddonDirs(_ context.Context, p *AddonPolicy) error {
	root := a.Scripts.Root
	var dirs []string
	for _, d := range standardAddonDirs(p.ID) {
		dirs = append(dirs, root.join(d))
	}
	res, err := Priv.OwnTree(p.ID, dirs, p.UID, priv.OwnTreeOptions{})
	if err == nil && res.Problem() != "" {
		err = errors.New(res.Problem())
	}
	if err != nil {
		return fmt.Errorf("the directories of %s: %w", p.ID, err)
	}
	if res.Fixed > 0 {
		slog.Info("addons: files given to the addon's user", "id", p.ID, "fixed", res.Fixed, "checked", res.Checked)
	}
	// D-52: the state the addon kept elsewhere on the userfs as root (B-62)
	return a.takeOverDataDirs(p, true)
}

// policyMu serialises the read-modify-write of the policy files once a writer runs in the
// background: SetPolicy and SetAddonOpenPorts hold it. The start's refreshes run before anything
// else can write and do not take it.
var policyMu sync.Mutex

// RefreshPolicyDropIns renders every stored policy's drop-in again and reloads systemd when one
// changed. Run at start after RefreshManifestRuntimes: what a drop-in says depends on the binary
// and on the box (D-46's certs group), not only on the stored policy, so an addon confined before
// an upgrade gets the new grants without anyone touching its policy. Returns the ids whose drop-in changed.
func (a *SystemdAddons) RefreshPolicyDropIns(ctx context.Context) []string {
	root := a.Scripts.Root
	certs := root.HasGroup(CertsGroup)
	var changed []string
	for id, p := range root.AddonPolicies() {
		want := renderDropIn(p, certs)
		path := filepath.Join(root.join(AddonPolicyDir), id+".conf")
		if readFile(path) == want {
			continue
		}
		if err := writeFileAtomic(path, []byte(want), 0o644); err == nil {
			changed = append(changed, id)
		}
	}
	if len(changed) > 0 {
		sort.Strings(changed)
		_, _ = a.Systemd.run(ctx, "daemon-reload")
	}
	return changed
}

// nextUID hands out uids from AddonUIDBase upwards, never reusing one from a stored policy.
func (a *SystemdAddons) nextUID() int {
	next := AddonUIDBase
	for _, p := range a.Scripts.Root.AddonPolicies() {
		if p.UID >= next {
			next = p.UID + 1
		}
	}
	return next
}

// ensureUser creates the addon's user and group through the privilege helper's own operation
// (B-54): two lines appended in place to /etc/passwd and /etc/group, idempotent. Busybox
// addgroup/adduser did this until 2026-09-09; they write "/etc/group+" beside the file and
// rename it, which fails on the image, where the two files are bind mounts of
// /run/openccu-lite/* over a read-only /etc (occu-etc-writable): the temporary file cannot be
// created there, and a rename onto a mount point would be EBUSY anyway. The in-place edit
// works on every product - the container and the OVA have a writable /etc and never noticed.
func (a *SystemdAddons) ensureUser(p *AddonPolicy) error {
	root := a.Scripts.Root
	if err := Priv.AddAddonUser(root.join("/etc/passwd"), root.join("/etc/group"), p.User, p.UID); err != nil {
		return fmt.Errorf("addon user %s: %s", p.User, readOnlyEtcHint(err))
	}
	return nil
}

// readOnlyEtcHint names the cause when the account write hits the read-only rootfs. That was
// B-28 for months and the bare message - "read-only file system" - sends the reader to the wrong
// place: nothing is wrong with the request, the box is missing the unit that makes those two
// files writable (occu-etc-writable.service, which bind-mounts copies from /run).
func readOnlyEtcHint(err error) string {
	msg := err.Error()
	if strings.Contains(strings.ToLower(msg), "read-only file system") {
		return msg + " (occu-etc-writable.service makes /etc/passwd and /etc/group writable; " +
			"without it no addon can be confined and they run as root)"
	}
	return msg
}

// PolicyIDs returns the addons with a stored policy, sorted.
func (r Root) PolicyIDs() []string {
	var ids []string
	for id := range r.AddonPolicies() {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
