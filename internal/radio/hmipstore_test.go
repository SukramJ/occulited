package radio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openccu-lite B-298: hmipserver's metadata and link stores are repaired from a last good copy.

func storeBox(t *testing.T) (root string, d Detector, calls *[]string, lines *[]string) {
	t.Helper()
	fakeUsers(t)
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/config/crRFD/data"), 0o700); err != nil {
		t.Fatal(err)
	}
	calls = recordOwnership(t, root)
	var l []string
	lines = &l
	old := storeNow
	storeNow = func() time.Time { return time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { storeNow = old })
	return root, Detector{Root: root}, calls, lines
}

func logTo(lines *[]string) func(string, ...any) {
	return func(f string, a ...any) { *lines = append(*lines, fmt.Sprintf(f, a...)) }
}

func storeFile(t *testing.T, root, name, content string) string {
	t.Helper()
	p := filepath.Join(root, "etc/config/crRFD/data", name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readOr(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func TestHMIPStoreValidKeptAndCopied(t *testing.T) {
	root, d, calls, lines := storeBox(t)
	meta := storeFile(t, root, "metaData.conf", `{"metaData":{"a:b":"1"}}`)
	link := storeFile(t, root, "linkData.conf", `{"linkData":{}}`)
	guardHMIPStores(d, "stopped", logTo(lines))
	if readOr(t, meta) != `{"metaData":{"a:b":"1"}}` || readOr(t, link) != `{"linkData":{}}` {
		t.Fatalf("a valid store was changed: %q %q", readOr(t, meta), readOr(t, link))
	}
	if readOr(t, meta+".good") != `{"metaData":{"a:b":"1"}}` || readOr(t, link+".good") != `{"linkData":{}}` {
		t.Fatalf("no good copy: %q %q", readOr(t, meta+".good"), readOr(t, link+".good"))
	}
	for _, g := range []string{meta + ".good", link + ".good"} {
		if st, err := os.Stat(g); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", g, err, st)
		}
	}
	// the copy is the server's (the temp file is chowned before the rename)
	if !has(*calls, "chown /etc/config/crRFD/data/.metaData.conf.") || !has(*calls, " 8111:8111") {
		t.Fatalf("owner of the copy: %v", *calls)
	}
	if len(*lines) != 0 {
		t.Fatalf("a good store logs nothing: %v", *lines)
	}
	// unchanged: the copy is not rewritten (no flash write at every start)
	*calls = nil
	guardHMIPStores(d, "prep", logTo(lines))
	if len(*calls) != 0 {
		t.Fatalf("an unchanged copy was rewritten: %v", *calls)
	}
	// changed: the copy follows
	storeFile(t, root, "metaData.conf", `{"metaData":{"a:b":"2"}}`)
	guardHMIPStores(d, "prep", logTo(lines))
	if readOr(t, meta+".good") != `{"metaData":{"a:b":"2"}}` {
		t.Fatalf("the copy did not follow: %q", readOr(t, meta+".good"))
	}
	// no temp file left behind
	if m, _ := filepath.Glob(filepath.Join(root, "etc/config/crRFD/data/.*.tmp")); len(m) != 0 {
		t.Fatalf("temp files left: %v", m)
	}
}

func TestHMIPStoreBrokenRestored(t *testing.T) {
	for _, tc := range []struct{ name, content, what string }{
		{"empty", "", "empty"},
		{"blank", "  \n", "empty"},
		{"truncated", `{"metaData":{"a:b"`, "not valid JSON"},
		{"not an object", `[1,2]`, "not valid JSON"},
		{"null", `null`, "not valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, d, calls, lines := storeBox(t)
			meta := storeFile(t, root, "metaData.conf", tc.content)
			storeFile(t, root, "metaData.conf.good", `{"metaData":{"a:b":"1"}}`)
			guardHMIPStores(d, "prep", logTo(lines))
			if got := readOr(t, meta); got != `{"metaData":{"a:b":"1"}}` {
				t.Fatalf("not restored: %q", got)
			}
			if st, err := os.Stat(meta); err != nil || st.Mode().Perm() != 0o600 {
				t.Fatalf("mode: %v %v", err, st)
			}
			aside := meta + ".broken-20261002T200000Z"
			if got := readOr(t, aside); got != tc.content {
				t.Fatalf("broken file not kept: %q", got)
			}
			if !has(*calls, "chown /etc/config/crRFD/data/metaData.conf.broken-20261002T200000Z 8111:8111") || !has(*calls, "chown /etc/config/crRFD/data/.metaData.conf.") {
				t.Fatalf("owners: %v", *calls)
			}
			if len(*lines) != 1 || !strings.Contains((*lines)[0], "prep hmipserver: metaData.conf was "+tc.what) || !strings.Contains((*lines)[0], "restored from its last good copy") {
				t.Fatalf("log: %v", *lines)
			}
			// the link store had no file: nothing made, nothing logged
			if _, err := os.Lstat(filepath.Join(root, "etc/config/crRFD/data/linkData.conf")); !os.IsNotExist(err) {
				t.Fatalf("a missing store was created: %v", err)
			}
		})
	}
}

func TestHMIPStoreBrokenWithoutCopyMovedAside(t *testing.T) {
	root, d, _, lines := storeBox(t)
	link := storeFile(t, root, "linkData.conf", "")
	// a broken copy is no copy
	storeFile(t, root, "linkData.conf.good", "")
	guardHMIPStores(d, "stopped", logTo(lines))
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the broken file stayed: %v", err)
	}
	if _, err := os.Stat(link + ".broken-20261002T200000Z"); err != nil {
		t.Fatal(err)
	}
	if len(*lines) != 1 || !strings.Contains((*lines)[0], "stopped hmipserver: linkData.conf was empty (0 bytes) and there is no good copy: moved aside") {
		t.Fatalf("log: %v", *lines)
	}
}

func TestHMIPStoreBrokenKeepsThree(t *testing.T) {
	root, d, _, lines := storeBox(t)
	for _, s := range []string{"20260101T000000Z", "20260201T000000Z", "20260301T000000Z"} {
		storeFile(t, root, "metaData.conf.broken-"+s, "")
	}
	storeFile(t, root, "metaData.conf", "")
	guardHMIPStores(d, "prep", logTo(lines))
	m, _ := filepath.Glob(filepath.Join(root, "etc/config/crRFD/data/metaData.conf.broken-*"))
	if len(m) != 3 || filepath.Base(m[0]) != "metaData.conf.broken-20260201T000000Z" || filepath.Base(m[2]) != "metaData.conf.broken-20261002T200000Z" {
		t.Fatalf("broken copies: %v", m)
	}
}

func TestHMIPStoreLinksNotFollowed(t *testing.T) {
	root, d, _, lines := storeBox(t)
	secret := filepath.Join(root, "secret")
	if err := os.WriteFile(secret, []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(root, "etc/config/crRFD/data/metaData.conf")
	if err := os.Symlink(secret, meta); err != nil {
		t.Fatal(err)
	}
	guardHMIPStores(d, "prep", logTo(lines))
	if _, err := os.Lstat(meta + ".good"); !os.IsNotExist(err) {
		t.Fatalf("a link's target was copied: %v", err)
	}
	if st, err := os.Lstat(meta); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was touched: %v %v", err, st)
	}
	if len(*lines) != 1 || !strings.Contains((*lines)[0], "metaData.conf left alone: a link") {
		t.Fatalf("log: %v", *lines)
	}
	// a broken store whose good copy is a link: moved aside, the link's target not restored
	if err := os.Remove(meta); err != nil {
		t.Fatal(err)
	}
	storeFile(t, root, "metaData.conf", "")
	if err := os.Symlink(secret, meta+".good"); err != nil {
		t.Fatal(err)
	}
	*lines = nil
	guardHMIPStores(d, "prep", logTo(lines))
	if _, err := os.Lstat(meta); !os.IsNotExist(err) {
		t.Fatalf("restored from a link: %q", readOr(t, meta))
	}
}

// The prep and the stop step of the unit run the check.
func TestHMIPStorePrepAndStopped(t *testing.T) {
	fakeUsers(t)
	root, rec := boxRoot(t, map[string]string{"raw-uart": "GPIO@3f201000.serial"})
	recordOwnership(t, root)
	d := Detector{Root: root, Run: rec.run, GPIOLimit: 0, Sleep: func(time.Duration) {}}
	r, err := Run(context.Background(), root, d, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"dev/mmd_bidcos", "dev/mmd_hmip"} {
		if err := os.WriteFile(filepath.Join(root, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data := filepath.Join(root, "etc/config/crRFD/data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(data, "metaData.conf")
	if err := os.WriteFile(meta, []byte(`{"metaData":{"k":"v"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var lines []string
	if err := Stopped(context.Background(), d, "hmipserver", logTo(&lines)); err != nil {
		t.Fatal(err)
	}
	if readOr(t, meta+".good") != `{"metaData":{"k":"v"}}` {
		t.Fatalf("stopped: no good copy: %q", readOr(t, meta+".good"))
	}
	if err := os.WriteFile(meta, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Prep(context.Background(), d, "hmipserver", r.Render.Plan, logTo(&lines)); err != nil {
		t.Fatal(err)
	}
	if readOr(t, meta) != `{"metaData":{"k":"v"}}` {
		t.Fatalf("prep: not restored: %q (%v)", readOr(t, meta), lines)
	}
}
