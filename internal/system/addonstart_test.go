package system

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
)

func startFile(t *testing.T, r Root, id string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(r.join(AddonPolicyDir + "/" + id + ".start"))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// switches is an EarlyStart for tests: the global switch and the addons it is off for.
type switches struct {
	on  bool
	off []string
}

func (s *switches) fn(id string) bool { return s.on && !slices.Contains(s.off, id) }

// task 119: the declaration, the stored block and the manifest's word
func TestStartInTheRuntime(t *testing.T) {
	if !(&AddonRuntime{Start: "early"}).DeclaresEarlyStart() {
		t.Error("early")
	}
	for _, rt := range []*AddonRuntime{nil, {}, {Start: "late"}, {Start: "Early"}} {
		if rt.DeclaresEarlyStart() {
			t.Errorf("%+v declares it", rt)
		}
	}
	if m := MergeRuntime(&AddonRuntime{}, &AddonRuntime{Start: "early"}); m.Start != "early" {
		t.Errorf("the entry's word: %q", m.Start)
	}
	if m := MergeRuntime(&AddonRuntime{Start: "early"}, &AddonRuntime{Ports: []int{1}}); m.Start != "early" {
		t.Errorf("an entry that says nothing keeps the stored one: %q", m.Start)
	}
	if m := MergeRuntime(nil, &AddonRuntime{Start: "early"}); m.Start != "early" {
		t.Errorf("no policy block: %q", m.Start)
	}
}

// start alone is no statement of what the addon needs to run, like needs and session
func TestStartAloneLeavesUndeclared(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/passwd": "root:x:0:0::/:/bin/sh\n", "etc/group": "root:x:0:\n"})
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }})
	for _, c := range []struct {
		id         string
		rt         *AddonRuntime
		undeclared bool
	}{
		{"startonly", &AddonRuntime{Start: "early"}, true},
		{"startneeds", &AddonRuntime{Start: "early", Needs: needs("rfd", "hmipserver")}, true},
		{"startports", &AddonRuntime{Start: "early", Ports: []int{8090}}, false},
	} {
		writePolicy(t, r, AddonPolicy{ID: c.id, Mode: "confined", Source: "catalog", Runtime: c.rt})
		if got := a.PolicyView(c.id).Undeclared; got != c.undeclared {
			t.Errorf("%s: undeclared %v, want %v", c.id, got, c.undeclared)
		}
	}
}

// at start, after a fetch and after a switch: the file follows the declaration and both switches
func TestRefreshAddonStart(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/passwd": "root:x:0:0::/:/bin/sh\n", "etc/group": "root:x:0:\n"})
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }})
	writePolicy(t, r, AddonPolicy{ID: "hmm", Mode: "confined", Source: "catalog"})
	writePolicy(t, r, AddonPolicy{ID: "redmatic", Mode: "root", Source: "migrated"})
	writePolicy(t, r, AddonPolicy{ID: "stored", Mode: "confined", Source: "user", Runtime: &AddonRuntime{Start: "early"}})
	writePolicy(t, r, AddonPolicy{ID: "odd", Mode: "confined", Source: "user", Runtime: &AddonRuntime{Start: "sometimes"}})
	writePolicy(t, r, AddonPolicy{ID: "gone", Mode: "root", Source: "catalog"})
	if err := os.WriteFile(r.join(AddonPolicyDir+"/gone.start"), []byte("early\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// no manifests, no switches (nil = on): the stored blocks alone; an unknown value writes nothing
	if ids := a.RefreshAddonStart(); strings.Join(ids, ",") != "gone,stored" {
		t.Errorf("without manifests: %v", ids)
	}
	if got, ok := startFile(t, r, "stored"); !ok || got != "early\n" {
		t.Errorf("stored: %q %v", got, ok)
	}
	for _, id := range []string{"gone", "odd", "hmm"} {
		if got, ok := startFile(t, r, id); ok {
			t.Errorf("%s: %q", id, got)
		}
	}

	writeManifest(t, r, "hmm", &AddonRuntime{Start: "early", Needs: needs("rfd", "hmipserver")})
	writeManifest(t, r, "redmatic", &AddonRuntime{Start: "early"}) // a migrated root policy: the manifest's word still reaches it
	if ids := a.RefreshAddonStart(); strings.Join(ids, ",") != "hmm,redmatic" {
		t.Errorf("with the manifests: %v", ids)
	}
	if ids := a.RefreshAddonStart(); ids != nil {
		t.Errorf("second run: %v", ids)
	}
	if d, e := a.StartEarly("hmm"); !d || !e {
		t.Errorf("hmm: declared %v early %v", d, e)
	}
	if d, e := a.StartEarly("gone"); d || e {
		t.Errorf("gone: declared %v early %v", d, e)
	}

	// switched off for one addon: its file goes, the others stay
	sw := &switches{on: true, off: []string{"hmm"}}
	a.EarlyStart = sw.fn
	if ids := a.RefreshAddonStart(); strings.Join(ids, ",") != "hmm" {
		t.Errorf("hmm off: %v", ids)
	}
	if _, ok := startFile(t, r, "hmm"); ok {
		t.Error("hmm switched off kept its file")
	}
	if d, e := a.StartEarly("hmm"); !d || e {
		t.Errorf("hmm off: declared %v early %v", d, e)
	}
	// off for all: every file goes
	sw.on, sw.off = false, nil
	if ids := a.RefreshAddonStart(); strings.Join(ids, ",") != "redmatic,stored" {
		t.Errorf("all off: %v", ids)
	}
	// on again: back
	sw.on = true
	if ids := a.RefreshAddonStart(); strings.Join(ids, ",") != "hmm,redmatic,stored" {
		t.Errorf("on again: %v", ids)
	}
	for _, id := range []string{"hmm", "redmatic", "stored"} {
		if got, _ := startFile(t, r, id); got != "early\n" {
			t.Errorf("%s: %q", id, got)
		}
	}
}

// SetPolicy - an install from the catalogue, a policy change - writes the file with the needs, in
// both modes, and honours the switch
func TestSetPolicyWritesStart(t *testing.T) {
	r := rootWith(t, map[string]string{
		"etc/passwd":                    "root:x:0:0::/:/bin/sh\n",
		"etc/group":                     "root:x:0:\n",
		"usr/local/addons/hmm/.keep":    "",
		"usr/local/etc/config/rc.d/hmm": "#!/bin/sh\n",
	})
	run := func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run})
	if _, err := a.SetPolicy(t.Context(), "hmm", "root", "catalog", &AddonRuntime{Start: "early", Needs: needs("rfd", "hmipserver")}); err != nil {
		t.Fatal(err)
	}
	if got, _ := startFile(t, r, "hmm"); got != "early\n" {
		t.Errorf("root: %q", got)
	}
	if got, _ := needsFile(t, r, "hmm"); got != "rfd hmipserver\n" {
		t.Errorf("needs beside it: %q", got)
	}
	if p := r.ReadAddonPolicy("hmm"); p.Runtime == nil || p.Runtime.Start != "early" {
		t.Errorf("the stored block lost the start: %+v", p.Runtime)
	}
	// a mode change keeps it
	if _, err := a.SetPolicy(t.Context(), "hmm", "confined", "user", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := startFile(t, r, "hmm"); got != "early\n" {
		t.Errorf("confined: %q", got)
	}
	// switched off: the next policy write removes it
	a.EarlyStart = func(string) bool { return false }
	if _, err := a.SetPolicy(t.Context(), "hmm", "confined", "user", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := startFile(t, r, "hmm"); ok {
		t.Error("switched off, the file stayed")
	}
	// a block that no longer declares it
	a.EarlyStart = nil
	if _, err := a.SetPolicy(t.Context(), "hmm", "confined", "catalog", &AddonRuntime{Needs: needs("rfd")}); err != nil {
		t.Fatal(err)
	}
	if _, ok := startFile(t, r, "hmm"); ok {
		t.Error("an entry without start kept the file")
	}
}
