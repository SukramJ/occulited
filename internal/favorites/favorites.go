// Package favorites keeps the metadata store's favorite enum in step with the accounts (task
// 193): one enum "favorite", one node per account named after it, keyed by the account's stable
// id - so a favorites page is a node, membership is the object's enums as everywhere, and a
// rename of the account renames the node while its members stay.
//
// A node whose id is no account's is a waiting one: the CCU import files a CCU user's favorites
// under a node named after that user. When an account of that name appears, the waiting node's
// members move to the account's node and the waiting node goes.
//
// An account that goes takes its node along (occulited task 1): the node and its members'
// membership go in one metadata change, and the sync never creates it again. The sync knows an
// account went by the accounts it saw on its previous run - an account removed while occulited
// was not running leaves its node behind, as does a run that sees no accounts at all (users.json
// removed: setup again, and nothing is thrown away on that).
package favorites

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/hobbyquaker/occulited/internal/meta"
)

// EnumID is the enum's id.
const EnumID = "favorite"

// OrderNamespace and OrderKey are where an object's rank within a favorites page lives:
// meta.occulite.order["favorite/<account id>"] = <sparse rank>; absent sorts after the ranked,
// by name. The store does not interpret it; the App and this package's Rank do.
const (
	OrderNamespace = "occulite"
	OrderKey       = "order"
)

// Account is what the sync needs of an account.
type Account struct {
	ID   string
	Name string
}

// Sync makes the enum and the nodes match the accounts. It is safe to call often; a call that
// changes nothing writes nothing (the store's own rule).
type Sync struct {
	Store *meta.Store
	Log   *slog.Logger
	mu    sync.Mutex
	seen  map[string]bool // the account ids of the previous run
}

func (s *Sync) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Run reconciles once.
func (s *Sync) Run(accounts []Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.run(accounts)
}

// RunFrom reads the accounts under the sync's lock and reconciles: of two runs told at once, the
// one that read the accounts last runs last, so an older list never re-creates a deleted
// account's node.
func (s *Sync) RunFrom(accounts func() []Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.run(accounts())
}

func (s *Sync) run(accounts []Account) {
	doc := s.Store.Snapshot()
	e := doc.Enums[EnumID]
	if e == nil {
		if _, _, err := s.Store.CreateEnum(nil, EnumID, map[string]string{"de": "Favoriten", "en": "Favorites"}); err != nil {
			s.log().Warn("favorites: the enum could not be created", "err", err)
			return
		}
		doc = s.Store.Snapshot()
		e = doc.Enums[EnumID]
	}
	byID := map[string]*meta.Node{}
	for _, n := range e.Tree {
		byID[n.ID] = n
	}
	isAccount := map[string]bool{}
	for _, a := range accounts {
		if a.ID == "" {
			continue
		}
		isAccount[a.ID] = true
	}
	// an account seen on the previous run and gone now: its node goes, members detached, in one
	// change; no accounts at all is not taken as everyone gone
	if len(isAccount) > 0 {
		removed := false
		for id := range s.seen {
			if isAccount[id] || byID[id] == nil {
				continue
			}
			if _, _, err := s.Store.DeleteNode(nil, EnumID+"/"+id, true); err != nil {
				s.log().Warn("favorites: a deleted account's node could not be removed", "account", byID[id].Name, "err", err)
				continue
			}
			s.log().Info("favorites: a deleted account's favorites removed", "account", byID[id].Name, "node", id)
			removed = true
		}
		s.seen = isAccount
		if removed { // a removed node is no waiting one for an account of its name below
			doc = s.Store.Snapshot()
			e = doc.Enums[EnumID]
		}
	}
	for _, a := range accounts {
		if a.ID == "" {
			continue
		}
		if n, ok := byID[a.ID]; ok {
			if n.Name != a.Name {
				if _, _, err := s.Store.UpdateNode(nil, EnumID+"/"+a.ID, meta.NodePatch{Name: &a.Name}); err != nil {
					s.log().Warn("favorites: rename", "account", a.Name, "err", err)
				}
			}
			continue
		}
		if _, _, err := s.Store.CreateNode(nil, EnumID, nil, a.ID, a.Name, "", nil); err != nil {
			s.log().Warn("favorites: the account's node could not be created", "account", a.Name, "err", err)
			continue
		}
		// a waiting node of the same name (the CCU import's): its members move over
		for _, n := range e.Tree {
			if !isAccount[n.ID] && strings.EqualFold(n.Name, a.Name) {
				s.adopt(doc, n.ID, a.ID)
			}
		}
	}
}

// adopt moves every member of favorite/<from> to favorite/<to> and drops the waiting node.
func (s *Sync) adopt(doc *meta.Document, from, to string) {
	src, dst := EnumID+"/"+from, EnumID+"/"+to
	moved := 0
	for ref, o := range doc.Objects {
		has := false
		var enums []string
		for _, p := range o.Enums {
			if p == src {
				has = true
				enums = append(enums, dst)
			} else if p != dst {
				enums = append(enums, p)
			}
		}
		if !has {
			continue
		}
		m := o.Meta
		if raw, ok := m[OrderNamespace]; ok {
			var ns map[string]any
			if json.Unmarshal(raw, &ns) == nil {
				if order, ok := ns[OrderKey].(map[string]any); ok {
					if r, ok := order[src]; ok {
						order[dst] = r
						delete(order, src)
						if b, err := json.Marshal(ns); err == nil {
							m = map[string]json.RawMessage{}
							for k, v := range o.Meta {
								m[k] = v
							}
							m[OrderNamespace] = b
						}
					}
				}
			}
		}
		if _, _, err := s.Store.PutObject(nil, ref, o.Name, enums, m); err != nil {
			s.log().Warn("favorites: a member could not be moved", "ref", ref, "err", err)
			continue
		}
		moved++
	}
	if _, _, err := s.Store.DeleteNode(nil, src, true); err != nil {
		s.log().Warn("favorites: the waiting node could not be removed", "node", src, "err", err)
	}
	s.log().Info("favorites: a CCU user's favorites adopted by the account of that name", "node", from, "account", to, "moved", moved)
}

// Rank is an object's rank on a favorites page (node path), and whether it has one.
func Rank(o *meta.Object, path string) (float64, bool) {
	if o == nil || o.Meta == nil {
		return 0, false
	}
	raw, ok := o.Meta[OrderNamespace]
	if !ok {
		return 0, false
	}
	var ns struct {
		Order map[string]float64 `json:"order"`
	}
	if json.Unmarshal(raw, &ns) != nil {
		return 0, false
	}
	r, ok := ns.Order[path]
	return r, ok
}
