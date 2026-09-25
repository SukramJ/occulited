package system

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func needs(ids ...string) *[]string {
	out := append([]string{}, ids...)
	return &out
}

// task 94: the line the generator reads - none for [], the ids in a fixed order, and nothing for
// undeclared or for a declaration with an id this box does not know
func TestNeedsLine(t *testing.T) {
	cases := []struct {
		name    string
		rt      *AddonRuntime
		line    string
		ok      bool
		unknown []string
	}{
		{"no block", nil, "", false, nil},
		{"a block without needs", &AddonRuntime{Ports: []int{1883}}, "", false, nil},
		{"none", &AddonRuntime{Needs: needs()}, "none", true, nil},
		{"one", &AddonRuntime{Needs: needs("hmipserver")}, "hmipserver", true, nil},
		{"in the file's order, each once", &AddonRuntime{Needs: needs("hs485d", "hmipserver", "rfd", "hmipserver")}, "rfd hmipserver hs485d", true, nil},
		{"an unknown id", &AddonRuntime{Needs: needs("rfd", "cuxd")}, "", false, []string{"cuxd"}},
		{"an empty id", &AddonRuntime{Needs: needs("")}, "", false, []string{""}},
		{"a case that is not ours", &AddonRuntime{Needs: needs("RFD")}, "", false, []string{"RFD"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line, ok, unknown := needsLine(c.rt)
			if line != c.line || ok != c.ok || !slices.Equal(unknown, c.unknown) {
				t.Errorf("got %q %v %q, want %q %v %q", line, ok, unknown, c.line, c.ok, c.unknown)
			}
			if c.rt.NeedsLine() != c.line {
				t.Errorf("NeedsLine %q", c.rt.NeedsLine())
			}
		})
	}
}

// catalog-format.md: needs alone does not remove the undeclared marking, nor does session (task 88);
// every other block counts as before
func TestNeedsAloneLeavesUndeclared(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/passwd": "root:x:0:0::/:/bin/sh\n", "etc/group": "root:x:0:\n"})
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }})
	for _, c := range []struct {
		id         string
		rt         *AddonRuntime
		undeclared bool
	}{
		{"noblock", nil, true},
		{"needsonly", &AddonRuntime{Needs: needs()}, true},
		{"needsids", &AddonRuntime{Needs: needs("rfd", "hmipserver")}, true},
		{"emptyblock", &AddonRuntime{}, false},
		{"needsports", &AddonRuntime{Needs: needs(), Ports: []int{1883}}, false},
		{"needsroot", &AddonRuntime{Needs: needs("rfd"), Root: true}, false},
		{"needsgroups", &AddonRuntime{Needs: needs("rfd"), Groups: []string{"dialout"}}, false},
		{"needsdata", &AddonRuntime{Needs: needs(), DataDirs: []string{"/usr/local/hmm"}}, false},
		{"sessiononly", &AddonRuntime{Session: &AddonSession{HeaderSince: "9.7.3"}}, true},
		{"sessionneeds", &AddonRuntime{Needs: needs("rfd"), Session: &AddonSession{HeaderSince: "9.7.3"}}, true},
		{"sessionports", &AddonRuntime{Session: &AddonSession{HeaderSince: "9.7.3"}, Ports: []int{1880}}, false},
	} {
		writePolicy(t, r, AddonPolicy{ID: c.id, Mode: "confined", Source: "catalog", Runtime: c.rt})
		if got := a.PolicyView(c.id).Undeclared; got != c.undeclared {
			t.Errorf("%s: undeclared %v, want %v", c.id, got, c.undeclared)
		}
	}
	if !a.PolicyView("nopolicy").Undeclared {
		t.Error("an addon without a policy is undeclared")
	}
}

// absent and empty stay apart in the policy file, as on the wire
func TestNeedsInThePolicyFile(t *testing.T) {
	for _, c := range []struct {
		rt   AddonRuntime
		json string
	}{
		{AddonRuntime{}, `{}`},
		{AddonRuntime{Needs: needs()}, `{"needs":[]}`},
		{AddonRuntime{Needs: needs("rfd")}, `{"needs":["rfd"]}`},
	} {
		b, _ := json.Marshal(c.rt)
		if string(b) != c.json {
			t.Errorf("marshal %s, want %s", b, c.json)
		}
		var back AddonRuntime
		if err := json.Unmarshal(b, &back); err != nil || (back.Needs == nil) != (c.rt.Needs == nil) || (back.Needs != nil && len(*back.Needs) != len(*c.rt.Needs)) {
			t.Errorf("round trip of %s: %+v %v", b, back, err)
		}
	}
}

// the manifest's needs wins when it has one, whatever the policy's source; the stored one stays otherwise
func TestMergeRuntimeNeeds(t *testing.T) {
	if m := MergeRuntime(&AddonRuntime{Needs: needs("rfd")}, &AddonRuntime{Needs: needs()}); m.NeedsLine() != "none" {
		t.Errorf("the entry's word: %q", m.NeedsLine())
	}
	if m := MergeRuntime(&AddonRuntime{Needs: needs("rfd")}, &AddonRuntime{Ports: []int{1}}); m.NeedsLine() != "rfd" {
		t.Errorf("an entry that says nothing keeps the stored one: %q", m.NeedsLine())
	}
	if m := MergeRuntime(nil, &AddonRuntime{Needs: needs("hmipserver")}); m.NeedsLine() != "hmipserver" {
		t.Errorf("no policy block: %q", m.NeedsLine())
	}
	if m := MergeRuntime(&AddonRuntime{}, nil); m.Needs != nil {
		t.Errorf("neither: %+v", m.Needs)
	}
}

func needsFile(t *testing.T, r Root, id string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(r.join(AddonPolicyDir + "/" + id + ".needs"))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// SetPolicy - an install from the catalogue, a policy change - writes the file in both modes and
// removes it when the declaration says nothing usable
func TestSetPolicyWritesNeeds(t *testing.T) {
	r := rootWith(t, map[string]string{
		"etc/passwd":                     "root:x:0:0::/:/bin/sh\n",
		"etc/group":                      "root:x:0:\n",
		"usr/local/addons/mosq/.keep":    "",
		"usr/local/etc/config/rc.d/mosq": "#!/bin/sh\n",
	})
	run := func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run})

	// a root addon gets the file too: the order is not confinement
	if _, err := a.SetPolicy(t.Context(), "mosq", "root", "catalog", &AddonRuntime{Needs: needs()}); err != nil {
		t.Fatal(err)
	}
	if got, ok := needsFile(t, r, "mosq"); !ok || got != "none\n" {
		t.Errorf("root, []: %q %v", got, ok)
	}
	if p := r.ReadAddonPolicy("mosq"); p.Runtime == nil || p.Runtime.Needs == nil || len(*p.Runtime.Needs) != 0 {
		t.Errorf("the stored block lost the empty needs: %+v", p.Runtime)
	}
	// confined, and the entry names two processes
	if _, err := a.SetPolicy(t.Context(), "mosq", "confined", "catalog", &AddonRuntime{Needs: needs("hmipserver", "rfd")}); err != nil {
		t.Fatal(err)
	}
	if got, _ := needsFile(t, r, "mosq"); got != "rfd hmipserver\n" {
		t.Errorf("confined: %q", got)
	}
	// a mode change without a block keeps the stored declaration and its file
	if _, err := a.SetPolicy(t.Context(), "mosq", "root", "user", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := needsFile(t, r, "mosq"); got != "rfd hmipserver\n" {
		t.Errorf("after a mode change: %q", got)
	}
	// an id this box does not know: undeclared, the file goes
	if _, err := a.SetPolicy(t.Context(), "mosq", "root", "catalog", &AddonRuntime{Needs: needs("rfd", "cuxd")}); err != nil {
		t.Fatalf("an unknown id must not refuse the policy: %v", err)
	}
	if got, ok := needsFile(t, r, "mosq"); ok {
		t.Errorf("unknown id left %q", got)
	}
	// and a block with no needs at all: still no file, nothing to remove
	if _, err := a.SetPolicy(t.Context(), "mosq", "root", "catalog", &AddonRuntime{Ports: []int{1883}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := needsFile(t, r, "mosq"); ok {
		t.Error("a block without needs wrote a file")
	}
	// the stored manifest counts even when the stored block says nothing
	writeManifest(t, r, "mosq", &AddonRuntime{Needs: needs()})
	if _, err := a.SetPolicy(t.Context(), "mosq", "root", "user", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := needsFile(t, r, "mosq"); got != "none\n" {
		t.Errorf("from the manifest: %q", got)
	}
}

// at start every policy's file follows the declaration, whatever its source
func TestRefreshAddonNeeds(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/passwd": "root:x:0:0::/:/bin/sh\n", "etc/group": "root:x:0:\n"})
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }})
	writePolicy(t, r, AddonPolicy{ID: "mosq", Mode: "confined", Source: "catalog"})
	writePolicy(t, r, AddonPolicy{ID: "redmatic", Mode: "root", Source: "migrated"})
	writePolicy(t, r, AddonPolicy{ID: "hmm", Mode: "confined", Source: "user", Runtime: &AddonRuntime{Needs: needs("rfd", "hmipserver", "hs485d")}})
	writePolicy(t, r, AddonPolicy{ID: "gone", Mode: "root", Source: "catalog"})
	if err := os.WriteFile(r.join(AddonPolicyDir+"/gone.needs"), []byte("none\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// no manifests: the stored blocks alone
	if ids := a.RefreshAddonNeeds(); strings.Join(ids, ",") != "gone,hmm" {
		t.Errorf("without manifests: %v", ids)
	}
	if got, _ := needsFile(t, r, "hmm"); got != "rfd hmipserver hs485d\n" {
		t.Errorf("hmm: %q", got)
	}
	if _, ok := needsFile(t, r, "gone"); ok {
		t.Error("a declaration nobody makes any more left its file")
	}

	writeManifest(t, r, "mosq", &AddonRuntime{Needs: needs()})
	writeManifest(t, r, "redmatic", &AddonRuntime{Needs: needs("rfd", "hmipserver")})   // a migrated root policy: the manifest's order still reaches it
	writeManifest(t, r, "hmm", &AddonRuntime{Needs: needs("hmipserver", "hmip-wired")}) // an id this box does not know: the whole declaration is unusable
	if ids := a.RefreshAddonNeeds(); strings.Join(ids, ",") != "hmm,mosq,redmatic" {
		t.Errorf("with the manifests: %v", ids)
	}
	for id, want := range map[string]string{"mosq": "none\n", "redmatic": "rfd hmipserver\n"} {
		if got, _ := needsFile(t, r, id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
	if got, ok := needsFile(t, r, "hmm"); ok {
		t.Errorf("hmm kept %q beside an unknown id", got)
	}
	if ids := a.RefreshAddonNeeds(); ids != nil {
		t.Errorf("second run: %v", ids)
	}
}

// policyDirState is every file in the policy directory with its content and modification time.
func policyDirState(t *testing.T, r Root) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, _ := os.ReadDir(r.join(AddonPolicyDir))
	for _, e := range entries {
		p := filepath.Join(r.join(AddonPolicyDir), e.Name())
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(p)
		out[e.Name()] = st.ModTime().Format(time.RFC3339Nano) + " " + string(b)
	}
	return out
}

// the two policy writers wait for each other instead of writing a stale policy back
func TestPolicyWritersShareTheLock(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/passwd": "root:x:0:0::/:/bin/sh\n", "etc/group": "root:x:0:\n"})
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }})
	writePolicy(t, r, AddonPolicy{ID: "mosquitto", Mode: "root", Source: "catalog"})
	writeManifest(t, r, "mosquitto", &AddonRuntime{Needs: needs()})
	for _, c := range []struct {
		name string
		run  func()
	}{
		{"SetAddonOpenPorts", func() { _, _ = r.SetAddonOpenPorts("mosquitto", nil, nil) }},
		{"SetPolicy", func() { _, _ = a.SetPolicy(context.Background(), "mosquitto", "root", "catalog", nil) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			policyMu.Lock()
			done := make(chan struct{})
			go func() { c.run(); close(done) }()
			select {
			case <-done:
				t.Error("ran while another policy write held the lock")
			case <-time.After(50 * time.Millisecond):
			}
			policyMu.Unlock()
			<-done
		})
	}
}
