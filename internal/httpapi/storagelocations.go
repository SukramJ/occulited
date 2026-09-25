package httpapi

// openccu-lite task 228, phases 3 and 4: the one location picker. GET /storage/locations lists
// where a use may keep its files - the system's own storage (userfs), each USB stick by its label,
// each network share by its name - with its state, free space, what uses it, and for the use at
// hand whether it may be picked and why not. GET /storage/dirs completes a folder on one of them:
// directory names only, depth-limited, no links followed out of the location. What is stored is
// always the location id and a folder (internal/location), never a mount path.

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/backuptarget"
	"github.com/hobbyquaker/occulited/internal/location"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/shares"
	"github.com/hobbyquaker/occulited/internal/system"
)

func (a *SystemAPI) registerLocations(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeSystemRead, "GET "+p+"/storage/locations", a.locationsList)
	route(mux, auth.ScopeSystemRead, "GET "+p+"/storage/dirs", a.locationDirs)
}

// locationUse is one thing that keeps its files on a location.
type locationUse struct {
	Kind   string `json:"kind"` // journal, backup, store
	Name   string `json:"name,omitempty"`
	ID     string `json:"id,omitempty"`
	Folder string `json:"folder,omitempty"`
}

// locationView is one location for the picker.
type locationView struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	// State: present (the userfs, a stick plugged in), mounted, idle (a share mounted on use),
	// or a share's failure (unreachable, auth-failed, stale, …)
	State      string        `json:"state"`
	StateText  string        `json:"state_detail,omitempty"`
	FreeBytes  *int64        `json:"free_bytes,omitempty"`
	TotalBytes *int64        `json:"total_bytes,omitempty"`
	ReadOnly   bool          `json:"read_only,omitempty"`
	Uses       []locationUse `json:"uses"`
	// for the use asked about
	Allowed      bool   `json:"allowed"`
	Code         string `json:"code,omitempty"`
	Reason       string `json:"reason,omitempty"`
	UserfsPrefix string `json:"userfs_prefix,omitempty"`
	Fixed        bool   `json:"fixed,omitempty"`
	Default      string `json:"default,omitempty"`
}

func validUse(u string) bool {
	return u == location.UseJournal || u == location.UseBackup || u == location.UseStore
}

// locationUses answers every use of every location: the journal's target, the backup targets (a
// directory target by its location or its path's stick, a share target by its share), the database.
func (a *SystemAPI) locationUses(r *http.Request) map[string][]locationUse {
	out := map[string][]locationUse{}
	add := func(id string, u locationUse) { out[id] = append(out[id], u) }
	jc := a.Root.ReadJournalSetting()
	switch {
	case jc.TargetLabel != "":
		add("usb:"+jc.TargetLabel, locationUse{Kind: "journal", Folder: jc.TargetDir})
	case jc.TargetShare != "":
		add("share:"+jc.TargetShare, locationUse{Kind: "journal", Folder: jc.TargetDir})
	case jc.JournalWanted() != system.JournalRAM:
		add(location.KindUserfs, locationUse{Kind: "journal", Folder: "var/log/journal"})
	}
	if a.BackupTargets != nil {
		if list, err := a.BackupTargets.Store.List(); err == nil {
			for _, t := range list {
				u := locationUse{Kind: "backup", Name: t.Name, ID: t.ID}
				switch {
				case t.Kind == backuptarget.KindShare && t.Share != nil:
					u.Folder = t.Share.Folder
					add("share:"+t.Share.ID, u)
				case t.Kind == backuptarget.KindDirectory && t.Directory != nil && t.Directory.Location != "":
					if l, err := location.Parse(t.Directory.Location); err == nil {
						u.Folder = l.Folder
						add(l.ID(), u)
					}
				case t.Kind == backuptarget.KindDirectory && t.Directory != nil:
					// an older setting names a path: the stick it lies on, if any
					for _, s := range a.Root.USBSticks() {
						if s.LabelID != "" && (strings.HasPrefix(t.Directory.Path+"/", s.Mount+"/") || (s.Linked && strings.HasPrefix(t.Directory.Path+"/", "/media/usb0/"))) {
							add("usb:"+s.LabelID, u)
						}
					}
					if strings.HasPrefix(t.Directory.Path+"/", location.UserfsRoot+"/") {
						u.Folder = strings.TrimPrefix(t.Directory.Path, location.UserfsRoot+"/")
						add(location.KindUserfs, u)
					}
				}
			}
		}
	}
	if a.DataStore != nil {
		st := a.DataStore.Status()
		loc := st.Location
		if loc == "" {
			loc = st.DefaultLocation
		}
		if l, err := location.Parse(loc); err == nil {
			add(l.ID(), locationUse{Kind: "store", Folder: l.Folder})
		}
	}
	return out
}

// ShareUses answers what uses a share, for the shares' manager (its list and its removal).
func (a *SystemAPI) ShareUses(id string) []shares.Use {
	var out []shares.Use
	for _, u := range a.locationUses(nil)["share:"+id] {
		out = append(out, shares.Use{Kind: u.Kind, Name: u.Name, ID: u.ID})
	}
	return out
}

func (a *SystemAPI) locationsList(w http.ResponseWriter, r *http.Request) {
	use := r.URL.Query().Get("use")
	if !validUse(use) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: "use: journal, backup or store"})
		return
	}
	writeJSON(w, 200, map[string]any{"use": use, "locations": a.locations(r, use)})
}

func (a *SystemAPI) locations(r *http.Request, use string) []locationView {
	uses := a.locationUses(r)
	rule := func(v *locationView) {
		ru := location.RuleFor(use, v.Kind)
		v.Allowed, v.Code, v.Reason, v.UserfsPrefix, v.Fixed, v.Default = ru.Allowed, ru.Code, ru.Reason, ru.UserfsPrefix, ru.Fixed, ru.Default
		if v.Allowed && v.ReadOnly {
			v.Allowed, v.Code, v.Reason = false, "read-only", "it is mounted read-only"
		}
		if v.Uses = uses[v.ID]; v.Uses == nil {
			v.Uses = []locationUse{}
		}
	}
	out := []locationView{}
	uf := locationView{ID: location.KindUserfs, Kind: location.KindUserfs, Name: "userfs", Detail: location.UserfsRoot, State: "present"}
	var st syscall.Statfs_t
	if err := syscall.Statfs(a.Root.Path(location.UserfsRoot), &st); err == nil {
		free, total := int64(st.Bavail)*int64(st.Bsize), int64(st.Blocks)*int64(st.Bsize)
		uf.FreeBytes, uf.TotalBytes = &free, &total
	}
	rule(&uf)
	out = append(out, uf)
	for _, s := range a.Root.USBSticks() {
		name := s.Label
		if name == "" {
			name = s.LabelID
		}
		v := locationView{ID: "usb:" + s.LabelID, Kind: location.KindUSB, Name: name, Detail: strings.TrimSpace(s.Vendor + " " + s.Model), State: "present", ReadOnly: s.ReadOnly}
		free, total := s.Free, s.Total
		v.FreeBytes, v.TotalBytes = &free, &total
		rule(&v)
		if s.LabelID == "" {
			// a stick is named by its label: one without cannot be picked
			v.ID, v.Name = "usb:", strings.TrimSpace(s.Vendor+" "+s.Model)
			v.Allowed, v.Code, v.Reason = false, "no-label", "it has no label: format it on System → Storage, or give it one on another computer"
		}
		out = append(out, v)
	}
	if a.Shares != nil {
		views, _ := a.Shares.Views()
		for _, s := range views {
			v := locationView{ID: "share:" + s.ID, Kind: location.KindShare, Name: s.ID, Detail: s.Source(), State: s.State.State, StateText: s.State.Detail, ReadOnly: s.ReadOnly}
			if s.State.Mounted && s.State.TotalBytes > 0 {
				free, total := s.State.FreeBytes, s.State.TotalBytes
				v.FreeBytes, v.TotalBytes = &free, &total
			}
			rule(&v)
			if v.Allowed && s.State.State == shares.StateUnsupported {
				v.Allowed, v.Code, v.Reason = false, "unsupported", "shares cannot be mounted here"
			}
			out = append(out, v)
		}
	}
	return out
}

// dirsFailure classifies a failed folder listing (B-214): state "" for a folder that does not exist
// yet on a location that is there. A share that is not mounted after the access failed to mount -
// its unit's journal says why (unreachable, auth-failed); a mounted one answers with its error
// (EACCES: read-only, as the share's Test says for a root-squashed export).
func (a *SystemAPI) dirsFailure(kind, name string, err error) (state, msg string) {
	if kind == location.KindShare && a.Shares != nil {
		if _, mounted := a.Shares.Mounted(name); !mounted {
			state, detail := a.Shares.MountFailure(name)
			if state == "" {
				state, detail = shares.StateUnreachable, err.Error()
			}
			return state, "the share " + name + " could not be mounted: " + detail
		}
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, errDirLink) || errors.Is(err, syscall.ELOOP) {
		return "", ""
	}
	state = shares.ClassifyErrno(err)
	switch state {
	case shares.StateReadOnly:
		return state, "the system may not read this folder: " + err.Error()
	case shares.StateMounted:
		state = shares.StateError
	}
	return state, err.Error()
}

// errDirLink: a link on the way to the folder - never followed, so nothing to complete.
var errDirLink = errors.New("a link")

// dirsProbe bounds the listing of a share (its first access mounts it).
var dirsProbe shares.Prober

// maxDirs bounds an answer.
const maxDirs = 50

// locationDirs completes a folder: the directories under the prefix's parent whose names begin
// with its last part, as folders of the location. Directory names only, never a file's; a link is
// never followed (a stick or a share cannot lead the listing elsewhere).
func (a *SystemAPI) locationDirs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	use, id, prefix := q.Get("use"), q.Get("location"), strings.TrimLeft(q.Get("prefix"), "/")
	if !validUse(use) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: "use: journal, backup or store"})
		return
	}
	parent, partial := "", prefix
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		parent, partial = prefix[:i], prefix[i+1:]
	}
	if parent != "" && !location.ValidFolder(parent, false) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: "prefix: a folder of letters, digits, . _ -"})
		return
	}
	if strings.Count(prefix, "/") >= location.MaxDepth {
		writeJSON(w, 200, map[string]any{"dirs": []string{}, "exists": false})
		return
	}
	var root string
	kind, name, _ := strings.Cut(id, ":")
	switch {
	case id == location.KindUserfs:
		ru := location.RuleFor(use, location.KindUserfs)
		if ru.UserfsPrefix != "" && parent != ru.UserfsPrefix && !strings.HasPrefix(parent, ru.UserfsPrefix+"/") {
			// above the part of the userfs the use may name: its root is the only answer
			writeJSON(w, 200, map[string]any{"dirs": []string{ru.UserfsPrefix}, "exists": false})
			return
		}
		root = a.Root.Path(location.UserfsRoot)
	case kind == location.KindUSB && name != "":
		s, ok := a.Root.USBStickByLabel(name)
		if !ok {
			writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: "the USB stick " + name + " is not plugged in"})
			return
		}
		root = a.Root.Path(s.Mount)
	case kind == location.KindShare && name != "" && a.Shares != nil:
		if _, ok, _ := a.Shares.Store.Get(name); !ok {
			writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: "no share " + name})
			return
		}
		root = a.Root.Path(location.SharePath(name, ""))
	default:
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: "location: userfs, usb:<label> or share:<name>"})
		return
	}
	var dirs []string
	exists := false
	add := func(n string, isDir bool) {
		if !isDir || strings.HasPrefix(n, ".") || n == "lost+found" {
			return
		}
		if !strings.HasPrefix(strings.ToLower(n), strings.ToLower(partial)) || !location.ValidFolder(n, false) {
			return
		}
		if n == partial {
			exists = true
		}
		dirs = append(dirs, path.Join(parent, n))
	}
	list := func() error {
		base := filepath.Join(root, filepath.FromSlash(parent))
		if kind == location.KindShare {
			// B-217: as root through the helper - on a root-squashed export the folders root made
			// are nobody's and occulited's own user cannot enter them; the helper refuses a folder
			// reached through a link
			entries, err := priv.ListShare(r.Context(), a.Shares.Helper(), base)
			if err != nil {
				return err
			}
			for _, e := range entries {
				add(e.Name, e.Dir && !e.Link)
			}
			return nil
		}
		// no link on the way: every part of the parent is a directory of its own
		for p := base; p != root && strings.HasPrefix(p, root); p = filepath.Dir(p) {
			fi, err := os.Lstat(p)
			if err != nil {
				return err
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				return errDirLink
			}
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			return err
		}
		for _, e := range entries {
			add(e.Name(), e.IsDir() && e.Type()&os.ModeSymlink == 0)
		}
		return nil
	}
	var err error
	if kind == location.KindShare {
		err = dirsProbe.DoWithin("dirs:"+name, shares.MountWait, list)
	} else {
		err = list()
	}
	if errors.Is(err, shares.ErrStale) {
		writeJSON(w, http.StatusServiceUnavailable, apiError{Error: "stale", Message: "the share does not answer"})
		return
	}
	if err != nil {
		// B-214: only a folder that does not exist yet is "nothing there" (it is made on first use);
		// a share that did not mount, a folder the system may not read, anything else is said
		if state, msg := a.dirsFailure(kind, name, err); state != "" {
			writeJSON(w, http.StatusBadGateway, apiError{Error: state, Message: msg, Detail: map[string]any{"state": state, "error": err.Error()}})
			return
		}
		dirs = nil
	} else if partial == "" && parent != "" {
		// "journal/": the folder itself is there, its children are the completion
		exists = true
	}
	sort.Strings(dirs)
	if len(dirs) > maxDirs {
		dirs = dirs[:maxDirs]
	}
	if dirs == nil {
		dirs = []string{}
	}
	writeJSON(w, 200, map[string]any{"dirs": dirs, "exists": exists})
}
