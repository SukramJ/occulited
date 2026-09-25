package backuptarget

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hobbyquaker/occulited/internal/netmount"
	"github.com/hobbyquaker/occulited/internal/shares"
)

// Migrated is one task-86 NFS/SMB target that became a share target (task 228).
type Migrated struct {
	Target string `json:"target"`
	Name   string `json:"name"`
	Share  string `json:"share"`
	Folder string `json:"folder"`
	// OldMount is the target's own mount id when the share got another name: its units go.
	OldMount string `json:"old_mount,omitempty"`
}

// shareName makes a share's name from a target's name: lower-case letters and digits, starting
// with a letter, 16 at most; "" when nothing usable is left.
func shareName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	n := strings.TrimLeft(b.String(), "0123456789")
	if len(n) > 16 {
		n = n[:16]
	}
	if !netmount.ValidID(n) {
		return ""
	}
	return n
}

// MigrateMounts turns every task-86 NFS or SMB target into a share of System → Storage and a
// target of kind share on it (task 228: shares are defined once). It runs at every start and does
// nothing once there are none. The share is named after the target (NAS → nas), or keeps the
// target's own id when that name is taken; the target keeps its id, its settings, its results
// and its subdirectory (now the share's folder). An SMB password moves from the target's
// credentials file into the share's. The caller renders the new share's units and removes the
// old ones when OldMount is set.
func (s *Store) MigrateMounts(sh *shares.Store) ([]Migrated, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load()
	if err != nil {
		return nil, err
	}
	var out []Migrated
	changed := false
	for i, t := range c.Targets {
		if t.Kind != KindNFS && t.Kind != KindCIFS {
			continue
		}
		spec, ok := t.Mount()
		if !ok {
			continue
		}
		taken := map[string]bool{}
		existing, err := sh.List()
		if err != nil {
			return out, err
		}
		byID := map[string]shares.Share{}
		for _, e := range existing {
			taken[e.ID] = true
			byID[e.ID] = e
		}
		for _, o := range c.Targets {
			if o.ID != t.ID && (o.Kind == KindNFS || o.Kind == KindCIFS) {
				taken[o.ID] = true
			}
		}
		// the share's name: after the target (NAS → nas), else the target's own id. A share of that
		// name with the same source is one an earlier run made before it stopped: taken over. Any
		// other share - one the user added for the same server, say, with its own account - is
		// never reused.
		name, reuse := "", false
		for _, cand := range []string{shareName(t.Name), t.ID} {
			if cand == "" {
				continue
			}
			if e, ok := byID[cand]; ok {
				if e.Kind == spec.Kind && e.Server == spec.Server && e.Path == spec.Path {
					name, reuse = cand, true
					break
				}
				continue
			}
			if !taken[cand] {
				name = cand
				break
			}
		}
		if name == "" {
			return out, fmt.Errorf("target %s: no free name for its share", t.ID)
		}
		share := shares.Share{ID: name, Kind: spec.Kind, Server: spec.Server, Path: spec.Path, Version: spec.Version, Seal: spec.Seal, Created: t.Created}
		credPath := filepath.Join(s.SecretDir(t.ID), CredFile)
		if t.Kind == KindCIFS {
			share.User, share.Domain = t.CIFS.User, t.CIFS.Domain
			pw := credValue(readTrim(credPath), "password")
			share.Password = &pw
		}
		// the share store's own checks, but not its Taken: the target being migrated is the one
		// that holds the name under /media/net until it is saved
		if !reuse {
			if _, err := (&shares.Store{Dir: sh.Dir}).Create(share); err != nil {
				return out, fmt.Errorf("target %s: %w", t.ID, err)
			}
		}
		m := Migrated{Target: t.ID, Name: t.Name, Share: name, Folder: t.Subdir}
		if name != t.ID {
			m.OldMount = t.ID
		}
		c.Targets[i].Kind = KindShare
		c.Targets[i].Share = &ShareRef{ID: name, Folder: t.Subdir}
		c.Targets[i].Subdir = ""
		c.Targets[i].NFS, c.Targets[i].CIFS = nil, nil
		out = append(out, m)
		changed = true
	}
	if !changed {
		return nil, nil
	}
	if err := s.save(c); err != nil {
		return out, err
	}
	// the password lives in the share's file now; the target keeps its results
	for _, m := range out {
		_ = os.Remove(filepath.Join(s.SecretDir(m.Target), CredFile))
	}
	return out, nil
}
