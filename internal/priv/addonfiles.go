package priv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/addonunit"
)

// The named operations on what root runs or obeys (openccu-lite B-293, rootobeyed.go): the addon
// policy files rendered from checked data, an addon's rc.d entry enabled or disabled, its rc.d
// entry and web link removed at the uninstall, and the certificate removed. Each takes exactly the
// paths of its kind; none takes content the helper would write as it is.

const (
	opAddonPolicyFile = "addon-policy-file"
	opAddonEnable     = "addon-enable"
	opAddonRemove     = "addon-remove"
	opRemoveCert      = "removecert"
)

// AddonPolicyFile asks the helper to write or remove one policy file.
func (c Client) AddonPolicyFile(path string, f addonunit.File) error {
	return c.fileOp(request{Op: opAddonPolicyFile, Path: path, PolicyFile: &f})
}

// SetAddonEnabled asks the helper to set or clear the executable bits of an rc.d entry.
func (c Client) SetAddonEnabled(path string, enabled bool) error {
	return c.fileOp(request{Op: opAddonEnable, Path: path, Enabled: enabled})
}

// RemoveAddonEntry asks the helper to remove an addon's rc.d entry, its script and its web link.
func (c Client) RemoveAddonEntry(rcd, www string, whole bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := c.call(ctx, request{Op: opAddonRemove, Path: rcd, WWW: www, Recursive: whole})
	return res.Names, err
}

// RemoveCertificate asks the helper to remove the live certificate and its markers.
func (c Client) RemoveCertificate(path string) error {
	return c.fileOp(request{Op: opRemoveCert, Path: path})
}

// AddonPolicyFile writes the file's text as rendered by addonunit, 0644, or removes it.
func (l Local) AddonPolicyFile(path string, f addonunit.File) error {
	id, kind, ok := splitPolicyFile(path)
	if !ok {
		return fmt.Errorf("%s: not an addon policy file", path)
	}
	text, remove, err := f.Content(kind, id)
	if err != nil {
		return err
	}
	if remove {
		return l.Remove(path)
	}
	return l.WriteFile(path, []byte(text), 0o644)
}

// splitPolicyFile is <id> and the kind of a policy file's name.
func splitPolicyFile(path string) (id, kind string, ok bool) {
	name := filepath.Base(path)
	for _, k := range addonunit.Kinds {
		if base, cut := strings.CutSuffix(name, k); cut && addonunit.IDRe.MatchString(base) {
			return base, k, true
		}
	}
	return "", "", false
}

// SetAddonEnabled sets (0755 added) or clears (0111 taken) the executable bits of a regular file and
// changes nothing else of its mode.
func (l Local) SetAddonEnabled(path string, enabled bool) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", path)
	}
	mode := st.Mode().Perm()
	if enabled {
		mode |= 0o755
	} else {
		mode &^= 0o111
	}
	return l.Chmod(path, mode)
}

// RemoveAddonEntry removes rcd, rcd.script and www - www with everything in it only when whole,
// otherwise only a link or an empty directory - and answers the paths that were there and went.
func (l Local) RemoveAddonEntry(rcd, www string, whole bool) ([]string, error) {
	var removed []string
	var errs []error
	paths := []string{www}
	if rcd != "" {
		paths = []string{rcd, rcd + ".script", www}
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		fi, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if p == www && whole && fi.IsDir() {
			err = l.RemoveAll(p)
		} else {
			err = l.Remove(p)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, p)
	}
	return removed, errors.Join(errs...)
}

// RemoveCertificate removes the markers first - with one in place S50lighttpd's check_certificate
// would leave the file alone - then the file; a missing one is not an error.
func (l Local) RemoveCertificate(path string) error {
	for _, p := range []string{path + ManagedSuffix, path + MarkerSuffix, path} {
		if err := l.Remove(p); err != nil {
			return err
		}
	}
	return nil
}

// addonPolicyFileAllowed: one of the policy files root obeys, of an addon id's shape.
func (p Policy) addonPolicyFileAllowed(path string) bool {
	rel, ok := p.rel(path)
	if !ok || strings.Contains(rel, "..") {
		return false
	}
	id, _, ok := p.addonPolicyFile(rel)
	return ok && addonunit.IDRe.MatchString(id)
}

// rcEntryID is the addon id of an rc.d entry path (either spelling), "" when it is none: a name of
// the id's shape directly in the rc.d directory, not the addon's own script beside the wrapper.
func (p Policy) rcEntryID(path string) string {
	rel, ok := p.rel(path)
	if !ok || strings.Contains(rel, "..") || p.AddonRCDir == "" {
		return ""
	}
	for _, d := range []string{p.AddonRCDir, otherSpelling(p.AddonRCDir)} {
		if d == "" {
			continue
		}
		id, found := strings.CutPrefix(rel, strings.TrimSuffix(d, "/")+"/")
		if found && addonunit.IDRe.MatchString(id) && !strings.HasSuffix(id, ".script") {
			return id
		}
	}
	return ""
}

// wwwEntryID is the addon id of a web tree entry in the config directory (either spelling), "" when
// it is none.
func (p Policy) wwwEntryID(path string) string {
	rel, ok := p.rel(path)
	if !ok || strings.Contains(rel, "..") {
		return ""
	}
	for _, d := range []string{"/usr/local/etc/config/addons/www/", "/etc/config/addons/www/"} {
		id, found := strings.CutPrefix(rel, d)
		if found && addonunit.IDRe.MatchString(id) {
			return id
		}
	}
	return ""
}

// certFile: the live certificate itself (not a marker), in either spelling of the config directory.
func (p Policy) certFile(path string) bool {
	rel, ok := p.rel(path)
	if !ok || strings.Contains(rel, "..") || len(p.CertPaths) == 0 {
		return false
	}
	c := p.CertPaths[0]
	return rel == c || (otherSpelling(c) != "" && rel == otherSpelling(c))
}

// doNamedRootObeyed serves the four operations; handled is false for any other.
func (s *Server) doNamedRootObeyed(req request) (res response, handled bool) {
	ops := s.ops()
	fail := func(err error) response {
		if err != nil {
			return response{Error: err.Error()}
		}
		return response{OK: true}
	}
	// the path as given and as it resolves through the image's own links both have to be the
	// operation's; the operation then runs on the resolved path, following nothing
	resolve := func(path string, final bool, allowed func(string) bool) (string, error) {
		return s.resolved(path, final, allowed)
	}
	switch req.Op {
	case opAddonPolicyFile:
		if req.PolicyFile == nil {
			return refuse("addon policy file: no content"), true
		}
		path, err := resolve(req.Path, true, s.Policy.addonPolicyFileAllowed)
		if err != nil {
			s.log("helper: refused the addon policy file %s: %v", req.Path, err)
			return refuse("addon policy file " + req.Path), true
		}
		id, kind, _ := splitPolicyFile(path)
		// checked here as well as in the operation: this is the boundary
		if _, _, err := req.PolicyFile.Content(kind, id); err != nil {
			s.log("helper: refused the addon policy file %s: %v", req.Path, err)
			return refuse("addon policy file " + req.Path + ": " + err.Error()), true
		}
		return fail(ops.AddonPolicyFile(path, *req.PolicyFile)), true
	case opAddonEnable:
		isEntry := func(p string) bool { return s.Policy.rcEntryID(p) != "" }
		if !isEntry(req.Path) {
			s.log("helper: refused enabling %s", req.Path)
			return refuse("addon entry " + req.Path), true
		}
		// the entry may be a link the installer made (root's, in root's directory): the bits of
		// what it leads to change, as a chmod of the entry always did
		path, err := s.Policy.resolve(req.Path, true)
		if err != nil {
			s.log("helper: refused enabling %s: %v", req.Path, err)
			return refuse("addon entry " + req.Path + ": " + err.Error()), true
		}
		return fail(ops.SetAddonEnabled(path, req.Enabled)), true
	case opAddonRemove:
		// the two names need not be the same addon's: mediola's NEO Server is rc.d/97NeoServer with
		// the web entry mediola. Either may be left out, not both. Removing them gives nothing
		// root runs to the daemon; an uninstall does the same for any addon.
		isRC := func(p string) bool { return s.Policy.rcEntryID(p) != "" }
		isWWW := func(p string) bool { return s.Policy.wwwEntryID(p) != "" }
		if (req.Path == "" && req.WWW == "") || (req.Path != "" && !isRC(req.Path)) || (req.WWW != "" && !isWWW(req.WWW)) {
			s.log("helper: refused removing the addon entry %s, %s", req.Path, req.WWW)
			return refuse("addon entry " + req.Path + " " + req.WWW), true
		}
		var rcd, www string
		var err error
		if req.Path != "" {
			if rcd, err = resolve(req.Path, false, isRC); err != nil {
				return refuse("addon entry " + req.Path + ": " + err.Error()), true
			}
		}
		if req.WWW != "" {
			if www, err = resolve(req.WWW, false, isWWW); err != nil {
				return refuse("addon web entry " + req.WWW + ": " + err.Error()), true
			}
		}
		removed, err := ops.RemoveAddonEntry(rcd, www, req.Recursive)
		// the answer names the paths as the daemon gave them
		names := make([]string, 0, len(removed))
		for _, r := range removed {
			switch {
			case r == "":
			case r == rcd:
				names = append(names, req.Path)
			case r == rcd+".script":
				names = append(names, req.Path+".script")
			case r == www:
				names = append(names, req.WWW)
			}
		}
		if err != nil {
			return response{Error: err.Error(), Names: names}, true
		}
		return response{OK: true, Names: names}, true
	case opRemoveCert:
		path, err := resolve(req.Path, false, s.Policy.certFile)
		if err != nil {
			s.log("helper: refused the certificate removal %s: %v", req.Path, err)
			return refuse("certificate path " + req.Path), true
		}
		return fail(ops.RemoveCertificate(path)), true
	}
	return response{}, false
}
