package system

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Addons after a backup restore (openccu-lite task 146, D-106). createBackup.sh runs tar with
// --exclude-tag=.nobackup: the contents of every directory carrying that tag are left out, the
// directory and the tag itself are kept. That is where an addon keeps its program (bin/, www/,
// app/, lib/), so after a restore the addon is back by its rc.d entry, its hm_addons.cfg line,
// its settings and data - and every tagged directory holds nothing but its tag, its unit fails,
// and nothing said why. That empty tagged directory is the signal: an addon whose tagged
// directories all hold only the tag has lost its program files and needs reinstalling; the page
// lists it with a Reinstall button when the catalogue knows it. A second signal, for an addon
// without tags, is the addon-rc wrapper's <id>.script link pointing at a file that is not there.
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

// taggedDirs lists the directories under the addon's directory that carry a .nobackup tag,
// relative to it and sorted, and those among them that hold nothing besides the tag.
func (p *PayloadRecord) taggedDirs(id string) (tagged []string, emptied []string) {
	base := p.Root.join("/usr/local/addons/" + id)
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		hasTag := false
		for _, e := range entries {
			if e.Name() == ".nobackup" && !e.IsDir() {
				hasTag = true
			}
		}
		if hasTag {
			rel, _ := filepath.Rel(base, dir)
			tagged = append(tagged, rel)
			if len(entries) == 1 {
				emptied = append(emptied, rel)
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
	return tagged, emptied
}

// danglingScript: the addon-rc wrapper's <id>.script is a link whose target is gone; the target
// is returned then.
func (p *PayloadRecord) danglingScript(id string) (string, bool) {
	link := p.Root.join("/usr/local/etc/config/rc.d/" + id + ".script")
	target, err := os.Readlink(link)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(link); err == nil || !errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	return target, true
}

// Check says which of the installed addons (ids from rc.d; versions from the listing, for the
// dismissals) miss their program files: every tagged directory holds only its tag - or, with no
// tag at all, the wrapper's script link dangles. A dismissal holds only while the addon is
// missing at the version it was dismissed at; it is dropped when the payload is back, and for
// an addon no longer installed.
func (p *PayloadRecord) Check(ids []string, versions map[string]string) map[string]PayloadState {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.load()
	out := map[string]PayloadState{}
	installed := map[string]bool{}
	changed := false
	for _, id := range ids {
		installed[id] = true
		st := PayloadState{}
		tagged, emptied := p.taggedDirs(id)
		if len(tagged) > 0 {
			if len(emptied) == len(tagged) {
				st.Missing, st.Dirs = true, emptied
			}
		} else if target, ok := p.danglingScript(id); ok {
			st.Missing, st.Dirs = true, []string{target}
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

// Dismiss hides the reinstall hint for the addon at this version; the next Check drops it once
// the payload is back or the version changes.
func (p *PayloadRecord) Dismiss(id, version string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.load()
	f.Dismissed[id] = version
	return p.save(f)
}
