package system

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Addons after a backup restore (openccu-lite task 146, D-106). createBackup.sh runs tar with
// --exclude-tag=.nobackup: the contents of every directory carrying that tag are left out, the
// directory and the tag itself are kept. That is where an addon keeps its program (bin/, www/,
// app/, lib/), so after a restore the addon is back by its rc.d entry, its hm_addons.cfg line,
// its settings and data - and every tagged directory holds nothing but its tag, its unit failed,
// and nothing said why. That empty tagged directory is the signal: an addon whose tagged
// directories all hold only the tag has lost its program files and needs reinstalling; the page
// lists it with a Reinstall button when the catalogue knows it. A second signal is the addon-rc
// wrapper's <id>.script link pointing at a file that is not there, a third the addon's unit
// skipped by the fork's program check (openccu-lite B-267: ExecCondition=lite-addon-payload keeps
// such a unit from starting and failing; nothing reinstalls it automatically).
// The one thing kept on disk is the dismissals (PayloadFile in the state directory).

// PayloadFile is the dismissals' file in the state directory.
const PayloadFile = "addon-payload.json"

// payloadDepth is how deep under /usr/local/addons/<id> the tags are looked for: the addons put
// them at the first level (bin/.nobackup), a few one deeper (var/npm-cache/.nobackup).
const payloadDepth = 2

// PayloadState is what the API says about one addon's program files.
type PayloadState struct {
	// Missing: every .nobackup directory of the addon holds nothing but the tag (or, without
	// tags, the rc.d <id>.script link points at a file that is not there).
	Missing bool
	// Dirs are the emptied directories, relative to the addon's directory ("bin", "www"), or the
	// dangling script's target for the fallback.
	Dirs []string
	// Dismissed: the user has dismissed the reinstall hint for this addon at this version.
	Dismissed bool
}

type payloadFile struct {
	Dismissed map[string]string `json:"dismissed"` // id -> the version dismissed at
}

// PayloadRecord checks the addons' program files; Path is the dismissals' JSON file.
type PayloadRecord struct {
	Root Root
	Path string
	mu   sync.Mutex
}

func (p *PayloadRecord) load() payloadFile {
	f := payloadFile{Dismissed: map[string]string{}}
	b, err := os.ReadFile(p.Path)
	if err != nil {
		return f
	}
	_ = json.Unmarshal(b, &f)
	if f.Dismissed == nil {
		f.Dismissed = map[string]string{}
	}
	return f
}

// save writes the dismissals as the daemon itself (the state directory is its own), a temporary
// file renamed into place; none left removes the file.
func (p *PayloadRecord) save(f payloadFile) error {
	if len(f.Dismissed) == 0 {
		err := os.Remove(p.Path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
		return err
	}
	tmp := p.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p.Path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// programDir: a tagged directory that holds the addon's program - not a cache or state (tmp/,
// cache/, anything under var/). RedMatic tags tmp/ and var/npm-cache/ even when its settings ask
// for its program in the backup, and those are empty on a working system (openccu-lite B-267).
func programDir(rel string) bool {
	switch {
	case rel == "tmp", rel == "cache", rel == "var", strings.HasPrefix(rel, "var/"),
		strings.HasSuffix(rel, "/tmp"), strings.HasSuffix(rel, "/cache"):
		return false
	}
	return true
}

// taggedDirs lists the program directories under the addon's directory that carry a .nobackup
// tag, relative to it and sorted: those it can read, those among them that hold nothing besides
// the tag, and the ones it cannot list (a confined addon's tree is 0751: the tag is seen through
// the x bit, the contents are not).
func (p *PayloadRecord) taggedDirs(id string) (tagged, emptied, unread []string) {
	base := p.Root.join("/usr/local/addons/" + id)
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		rel, _ := filepath.Rel(base, dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if depth > 0 && programDir(rel) {
				if fi, serr := os.Lstat(filepath.Join(dir, ".nobackup")); serr == nil && fi.Mode().IsRegular() {
					unread = append(unread, rel)
				}
			}
			return
		}
		hasTag := false
		for _, e := range entries {
			if e.Name() == ".nobackup" && !e.IsDir() {
				hasTag = true
			}
		}
		if hasTag {
			if programDir(rel) {
				tagged = append(tagged, rel)
				if len(entries) == 1 {
					emptied = append(emptied, rel)
				}
			}
			return // what is below a tagged directory is its payload, not another tag
		}
		for _, e := range entries {
			if e.IsDir() && depth < payloadDepth {
				walk(filepath.Join(dir, e.Name()), depth+1)
			}
		}
	}
	walk(base, 0)
	sort.Strings(tagged)
	sort.Strings(emptied)
	sort.Strings(unread)
	return tagged, emptied, unread
}

// danglingScript: the addon-rc wrapper's <id>.script is a link whose target is gone; the target
// is returned then.
func (p *PayloadRecord) danglingScript(id string) (string, bool) {
	link := p.Root.join("/usr/local/etc/config/rc.d/" + id + ".script")
	target, err := os.Readlink(link)
	if err != nil {
		return "", false
	}
	// an absolute target is read inside the root (the same path on the system itself)
	var resolved string
	if filepath.IsAbs(target) {
		resolved = p.Root.join(target)
	} else {
		resolved = filepath.Join(filepath.Dir(link), target)
	}
	if _, err := os.Stat(resolved); err == nil || !errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	return target, true
}

// Check says which of the installed addons (ids from rc.d; versions from the listing, for the
// dismissals) miss their program files. Three signals, any of them (openccu-lite B-267):
//   - skipped[id]: the addon's unit did not start because the fork's program check
//     (ExecCondition=lite-addon-payload, run as root) found the program missing - the one that
//     sees every directory;
//   - the wrapper's <id>.script link dangles (RedMatic and Mosquitto link it into their bin/);
//   - every tagged program directory this daemon can read holds only its tag (tmp/, cache/ and
//     var/ do not count).
//
// A dismissal holds only while the addon is missing at the version it was dismissed at; it is
// dropped when the payload is back, and for an addon no longer installed.
func (p *PayloadRecord) Check(ids []string, versions map[string]string, skipped map[string]bool) map[string]PayloadState {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.load()
	out := map[string]PayloadState{}
	installed := map[string]bool{}
	changed := false
	for _, id := range ids {
		installed[id] = true
		st := PayloadState{}
		tagged, emptied, unread := p.taggedDirs(id)
		target, dangling := p.danglingScript(id)
		if skipped[id] || dangling || (len(tagged) > 0 && len(emptied) == len(tagged)) {
			st.Missing = true
			st.Dirs = append(append([]string{}, emptied...), unread...)
			sort.Strings(st.Dirs)
			if len(st.Dirs) == 0 && dangling {
				st.Dirs = []string{target}
			}
		}
		if v, ok := f.Dismissed[id]; ok {
			if st.Missing && v == versions[id] {
				st.Dismissed = true
			} else {
				delete(f.Dismissed, id)
				changed = true
			}
		}
		out[id] = st
	}
	for id := range f.Dismissed {
		if !installed[id] {
			delete(f.Dismissed, id)
			changed = true
		}
	}
	if changed {
		if err := p.save(f); err != nil {
			slog.Warn("addons: the reinstall dismissals were not written", "path", p.Path, "err", err)
		}
	}
	return out
}

// ProgramMissing is Check's file-system answer for one addon, without the unit's signal and
// without touching the dismissals: the wrapper's script link dangles, or every tagged program
// directory this daemon can list holds only its tag. The ownership warning uses it (B-267): an
// addon a restore left without its program is not started, so its ownership step has not run
// either, and the reinstall - not "Fix ownership" - is what it needs.
func (p *PayloadRecord) ProgramMissing(id string) bool {
	tagged, emptied, _ := p.taggedDirs(id)
	_, dangling := p.danglingScript(id)
	return dangling || (len(tagged) > 0 && len(emptied) == len(tagged))
}

// Dismiss hides the reinstall hint for the addon at this version; the next Check drops it once
// the payload is back or the version changes.
func (p *PayloadRecord) Dismiss(id, version string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.load()
	f.Dismissed[id] = version
	return p.save(f)
}
