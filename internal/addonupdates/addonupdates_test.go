package addonupdates

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

type fake struct{}

func (fake) ListAddons(context.Context) ([]system.Addon, error) {
	return []system.Addon{{ID: "b", Name: "B", Version: "1.0", Update: "/addons/b/update.cgi"}, {ID: "a", Name: "A", Version: "2.0", Update: "/addons/a/update.cgi"}, {ID: "c", Name: "no check"}}, nil
}

func (fake) CheckUpdate(_ context.Context, a system.Addon, base string) system.UpdateInfo {
	return system.UpdateInfo{Installed: a.Version, Available: "9.9", UpdateAvailable: a.ID == "a", URL: base + a.Update}
}

func TestCheckAndState(t *testing.T) {
	s := &Service{Lister: fake{}, Checker: fake{}, WebBase: "http://127.0.0.1"}
	if st := s.State(); st.Available != 0 || len(st.Results) != 0 || st.Last != nil {
		t.Fatalf("empty: %+v", st)
	}
	s.Check(context.Background())
	st := s.State()
	if st.Available != 1 || len(st.Results) != 2 || st.Results[0].ID != "a" || st.Results[1].ID != "b" || st.Last == nil {
		t.Fatalf("%+v", st)
	}
	if st.Results[0].Info.URL != "http://127.0.0.1/addons/a/update.cgi" {
		t.Errorf("%+v", st.Results[0])
	}
	s.Trigger()
	s.Trigger() // a second trigger must not block
}

// box is an addon manager whose answers a test moves: the addons installed, what each one's check
// says, and a check that waits once until it is let go.
type box struct {
	mu        sync.Mutex
	addons    []system.Addon
	listErr   error
	available map[string]bool
	holdAt    string        // the check of this addon waits for release, once
	entered   chan struct{} // closed when that check waits
	release   chan struct{}
}

func newBox() *box {
	return &box{
		addons:    []system.Addon{{ID: "a", Name: "A", Update: "/addons/a/update.cgi"}, {ID: "b", Name: "B", Update: "/addons/b/update.cgi"}},
		available: map[string]bool{"a": true, "b": true},
	}
}

func (b *box) ListAddons(context.Context) ([]system.Addon, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]system.Addon(nil), b.addons...), b.listErr
}

func (b *box) CheckUpdate(_ context.Context, a system.Addon, _ string) system.UpdateInfo {
	b.mu.Lock()
	hold := b.holdAt != "" && b.holdAt == a.ID
	if hold {
		b.holdAt = ""
	}
	entered, release := b.entered, b.release
	b.mu.Unlock()
	if hold {
		close(entered)
		<-release
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return system.UpdateInfo{UpdateAvailable: b.available[a.ID]}
}

func (b *box) change(f func(*box)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f(b)
}

// what the state says about addon a: whether it has a result, and whether that names an update
func resultOf(s *Service, id string) (found, available bool) {
	for _, r := range s.State().Results {
		if r.ID == id {
			return true, r.Info.UpdateAvailable
		}
	}
	return false, false
}

// B-82: the count stayed at the daily run's answer from before the installs.
func TestRecheckAndForget(t *testing.T) {
	tests := []struct {
		name          string
		change        func(*box) // what the install or the uninstall did
		forget        bool       // Forget instead of Recheck
		wantFound     bool
		wantAvailable bool
		wantCount     int // b's update is still waiting in every case
	}{
		{name: "the update was installed: the check says so now", change: func(b *box) { b.available["a"] = false }, wantFound: true, wantCount: 1},
		{name: "the check still offers an update: it stays", change: func(*box) {}, wantFound: true, wantAvailable: true, wantCount: 2},
		{name: "the addon is gone: its result goes", change: func(b *box) { b.addons = b.addons[1:] }, wantCount: 1},
		{name: "it declares no check any more: its result goes", change: func(b *box) { b.addons[0].Update = "" }, wantCount: 1},
		{name: "the list cannot be read: the old result does not stay", change: func(b *box) { b.listErr = errors.New("rc.d unreadable") }, wantCount: 1},
		{name: "uninstalled: forgotten", change: func(*box) {}, forget: true, wantCount: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBox()
			s := &Service{Lister: b, Checker: b}
			s.Check(context.Background())
			if st := s.State(); st.Available != 2 {
				t.Fatalf("before: %+v", st)
			}
			b.change(tt.change)
			if tt.forget {
				s.Forget("a")
			} else {
				s.Recheck(context.Background(), "a")
			}
			found, available := resultOf(s, "a")
			if found != tt.wantFound || available != tt.wantAvailable {
				t.Errorf("a: found %v available %v, want %v and %v", found, available, tt.wantFound, tt.wantAvailable)
			}
			if st := s.State(); st.Available != tt.wantCount {
				t.Errorf("available %d, want %d: %+v", st.Available, tt.wantCount, st)
			}
		})
	}
	// on a service that never ran: a recheck stores the one result, a forget is harmless
	b := newBox()
	s := &Service{Lister: b, Checker: b}
	s.Forget("a")
	s.Recheck(context.Background(), "b")
	if st := s.State(); len(st.Results) != 1 || st.Available != 1 || st.Last != nil {
		t.Errorf("fresh: %+v", st)
	}
}

// A daily run that asked about an addon before its install must not put back the update the
// install just took away.
func TestCheckUnderWayKeepsARecheck(t *testing.T) {
	for _, tt := range []struct {
		name      string
		forget    bool
		wantFound bool
	}{
		{name: "rechecked while the run was under way", wantFound: true},
		{name: "forgotten while the run was under way", forget: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := newBox()
			s := &Service{Lister: b, Checker: b}
			// the run asks a (an update), then waits in b's check
			b.change(func(b *box) { b.holdAt, b.entered, b.release = "b", make(chan struct{}), make(chan struct{}) })
			done := make(chan struct{})
			go func() {
				s.Check(context.Background())
				close(done)
			}()
			<-b.entered
			// meanwhile a is installed (or removed) and its result moved
			b.change(func(b *box) { b.available["a"] = false })
			if tt.forget {
				s.Forget("a")
			} else {
				s.Recheck(context.Background(), "a")
			}
			close(b.release)
			<-done
			found, available := resultOf(s, "a")
			if found != tt.wantFound || available {
				t.Errorf("a after the run: found %v available %v, want found %v and no update", found, available, tt.wantFound)
			}
			if found, available := resultOf(s, "b"); !found || !available {
				t.Errorf("b: the run's own answer is kept: found %v available %v", found, available)
			}
			// the next run no longer defers to it
			b.change(func(b *box) { b.available["a"] = true })
			s.Check(context.Background())
			if found, available := resultOf(s, "a"); !found || !available {
				t.Errorf("a in the next run: found %v available %v", found, available)
			}
		})
	}
}
