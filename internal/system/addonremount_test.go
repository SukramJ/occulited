package system

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRemountUnits(t *testing.T) {
	out := `{"_SYSTEMD_UNIT":"addon-jp-hb-devices-addon.service"}
{"_SYSTEMD_UNIT":"addon-jp-hb-devices-addon.service"}
{"_SYSTEMD_UNIT":"occu-init-system.service"}
not json
{"_SYSTEMD_UNIT":"addon-.service"}
{"_SYSTEMD_UNIT":"addon-cuxd.socket"}

{"_SYSTEMD_UNIT":"addon-cuxd.service"}
`
	got := remountUnits([]byte(out))
	if strings.Join(got, ",") != "jp-hb-devices-addon,cuxd" {
		t.Errorf("units: %v", got)
	}
	if got := remountUnits(nil); len(got) != 0 {
		t.Errorf("no output: %v", got)
	}
}

func TestRemountRefused(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/passwd": "root:x:0:0::/:/bin/sh\n"})
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }})
	calls := 0
	var args []string
	a.Journalctl = func(_ context.Context, a ...string) ([]byte, error) {
		calls++
		args = a
		if calls == 1 {
			return nil, errors.New("exit status 1") // B-168: nothing matched
		}
		return []byte(`{"_SYSTEMD_UNIT":"addon-hb.service"}` + "\n"), nil
	}
	if got := a.RemountRefused(context.Background()); len(got) != 0 {
		t.Errorf("nothing matched: %v", got)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-b", "-o json", "--output-fields=_SYSTEMD_UNIT", "-g " + remountPattern} {
		if !strings.Contains(joined, want) {
			t.Errorf("journalctl args lack %q: %s", want, joined)
		}
	}
	// the empty answer is cached too: the pages poll every few seconds
	if got := a.RemountRefused(context.Background()); len(got) != 0 || calls != 1 {
		t.Errorf("cached: %v calls %d", got, calls)
	}
	a.remount.at = time.Now().Add(-remountCacheTTL - time.Second)
	if got := a.RemountRefused(context.Background()); !got["hb"] || len(got) != 1 || calls != 2 {
		t.Errorf("after the cache: %v calls %d", got, calls)
	}
	// the flags on the two lists
	svc := a.OverlayServicePolicy([]Service{{ID: "addon-hb", Kind: "addon"}, {ID: "addon-other", Kind: "addon"}, {ID: "rfd", Kind: "system"}})
	if !svc[0].RemountRefused || svc[1].RemountRefused || svc[2].RemountRefused {
		t.Errorf("services: %+v", svc)
	}
	list := a.overlayAddonPolicy([]Addon{{ID: "hb"}, {ID: "other"}})
	if !list[0].RemountRefused || list[1].RemountRefused {
		t.Errorf("addons: %+v", list)
	}
}

func TestPolicyViewMayMount(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/local/etc/config/addon-policy/mounter.json": `{"id":"mounter","mode":"root","source":"catalog","runtime":{"root":true,"capabilities":["CAP_SYS_ADMIN"]}}`,
		"usr/local/etc/config/addon-policy/plain.json":   `{"id":"plain","mode":"root","source":"migrated"}`,
		"usr/local/etc/config/addon-policy/caged.json":   `{"id":"caged","mode":"confined","uid":30001,"user":"addon-caged","runtime":{"capabilities":["CAP_SYS_ADMIN"]}}`,
	})
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }})
	a.Journalctl = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("exit status 1") }
	if v := a.PolicyView("mounter"); !v.MayMount {
		t.Errorf("a root addon declaring CAP_SYS_ADMIN may mount: %+v", v)
	}
	if v := a.PolicyView("plain"); v.MayMount {
		t.Errorf("a plain root addon may not: %+v", v)
	}
	// confined: the block grants the capability the confined way; it is not "root that may mount"
	if v := a.PolicyView("caged"); v.MayMount {
		t.Errorf("a confined addon is not marked: %+v", v)
	}
	// the stored manifest counts too, for a root policy without the capability stored
	writeManifest(t, r, "plain", &AddonRuntime{Root: true, Capabilities: []string{CapSysAdmin}})
	if v := a.PolicyView("plain"); !v.MayMount {
		t.Errorf("the entry's declaration marks it: %+v", v)
	}
	list := a.overlayAddonPolicy([]Addon{{ID: "mounter"}, {ID: "caged"}})
	if !list[0].MayMount || list[1].MayMount {
		t.Errorf("addons: %+v", list)
	}
}
