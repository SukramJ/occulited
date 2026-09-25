// Package addonupdates is the daily addon update check (task 10): what checkAddonUpdates.sh did
// from the crontab into a ReGa variable, done here into memory and served on the status page.
// Each installed addon's Update: CGI is asked once a day with a random offset, or on demand.
package addonupdates

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// Lister and Checker are the two halves of the addon manager the check needs.
type Lister interface {
	ListAddons(ctx context.Context) ([]system.Addon, error)
}

// Checker runs one addon's update check.
type Checker interface {
	CheckUpdate(ctx context.Context, a system.Addon, base string) system.UpdateInfo
}

// Result is one addon's last check.
type Result struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Checked time.Time         `json:"checked"`
	Info    system.UpdateInfo `json:"info"`
}

// Service keeps the last results.
type Service struct {
	Lister   Lister
	Checker  Checker
	WebBase  string
	Interval time.Duration // default 24 h
	// Daily says whether the scheduled checks run (task 244: the Addons page's *Check daily*); nil
	// = always. Trigger runs a check either way.
	Daily func() bool

	mu      sync.Mutex
	results map[string]Result
	last    time.Time
	trigger chan struct{}
	// touched is when Recheck or Forget last moved an addon's result: a full run that listed the
	// addons before that keeps what they left instead of its own older answer
	touched map[string]time.Time
}

// State is what the API serves.
type State struct {
	Last      *time.Time `json:"last,omitempty"`
	Available int        `json:"available"`
	Results   []Result   `json:"results"`
}

// Run checks once shortly after start, then daily with jitter, and whenever Trigger is called.
func (s *Service) Run(ctx context.Context) {
	if s.Interval == 0 {
		s.Interval = 24 * time.Hour
	}
	s.mu.Lock()
	if s.trigger == nil {
		s.trigger = make(chan struct{}, 1)
	}
	s.mu.Unlock()
	first := time.After(2*time.Minute + time.Duration(rand.Int64N(int64(3*time.Minute))))
	for {
		select {
		case <-ctx.Done():
			return
		case <-first:
			if s.Daily == nil || s.Daily() {
				s.Check(ctx)
			}
		case <-time.After(s.Interval + time.Duration(rand.Int64N(int64(2*time.Hour)))):
			if s.Daily == nil || s.Daily() {
				s.Check(ctx)
			}
		case <-s.trigger:
			s.Check(ctx)
		}
	}
}

// Trigger asks for a check now.
func (s *Service) Trigger() {
	s.mu.Lock()
	if s.trigger == nil {
		s.trigger = make(chan struct{}, 1)
	}
	t := s.trigger
	s.mu.Unlock()
	select {
	case t <- struct{}{}:
	default:
	}
}

// Check runs the checks now (synchronously).
func (s *Service) Check(ctx context.Context) {
	start := time.Now()
	list, err := s.Lister.ListAddons(ctx)
	if err != nil {
		slog.Warn("addon updates: list", "err", err)
		return
	}
	results := map[string]Result{}
	n := 0
	for _, a := range list {
		if a.Update == "" {
			continue
		}
		info := s.Checker.CheckUpdate(ctx, a, s.WebBase)
		results[a.ID] = Result{ID: a.ID, Name: a.Name, Checked: time.Now(), Info: info}
		if info.UpdateAvailable {
			n++
		}
	}
	s.mu.Lock()
	// an addon installed, updated or removed while this run was under way keeps what that step
	// left: this run asked about the addon before it changed
	for id, at := range s.touched {
		if !at.After(start) {
			delete(s.touched, id)
			continue
		}
		if r, ok := s.results[id]; ok {
			results[id] = r
		} else {
			delete(results, id)
		}
	}
	s.results, s.last = results, time.Now()
	s.mu.Unlock()
	slog.Info("addon updates: checked", "addons", len(results), "available", n)
}

// Recheck runs one addon's check again, after that addon was installed or updated. The Status
// page counts the updates the stored results name, and the daily run that stored them asked
// before the install: the count said "3 updates" for three addons just updated from the
// catalogue. An addon that is gone, or declares no check any more, loses its result - and so does
// one when the list cannot be read, because the old result is exactly what must not stay.
func (s *Service) Recheck(ctx context.Context, id string) {
	list, err := s.Lister.ListAddons(ctx)
	if err != nil {
		slog.Warn("addon updates: list", "err", err)
		s.Forget(id)
		return
	}
	for _, a := range list {
		if a.ID != id || a.Update == "" {
			continue
		}
		info := s.Checker.CheckUpdate(ctx, a, s.WebBase)
		s.store(id, &Result{ID: a.ID, Name: a.Name, Checked: time.Now(), Info: info})
		slog.Info("addon updates: checked again", "addon", id, "available", info.UpdateAvailable)
		return
	}
	s.Forget(id)
}

// Forget drops an addon's result: it was uninstalled.
func (s *Service) Forget(id string) {
	s.store(id, nil)
}

// store replaces one addon's result (nil removes it) and marks it as newer than a run under way.
func (s *Service) store(id string, r *Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.touched == nil {
		s.touched = map[string]time.Time{}
	}
	s.touched[id] = time.Now()
	if r == nil {
		delete(s.results, id)
		return
	}
	if s.results == nil {
		s.results = map[string]Result{}
	}
	s.results[id] = *r
}

// State returns the last results, sorted by id.
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{Results: []Result{}}
	if !s.last.IsZero() {
		last := s.last
		st.Last = &last
	}
	for _, r := range s.results {
		st.Results = append(st.Results, r)
		if r.Info.UpdateAvailable {
			st.Available++
		}
	}
	for i := 1; i < len(st.Results); i++ {
		for j := i; j > 0 && st.Results[j].ID < st.Results[j-1].ID; j-- {
			st.Results[j], st.Results[j-1] = st.Results[j-1], st.Results[j]
		}
	}
	return st
}
