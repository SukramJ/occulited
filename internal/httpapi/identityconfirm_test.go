package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

// openccu-lite task 317 (D-120): every route that writes, moves, replaces or deletes the HmIP
// identity files (crRFD/data/<SGTIN>.ap, .apkx, .bbkx) refuses a request without the user's
// confirmation - 400 confirm, and 400 hostname where the host name is typed - before the service
// is asked; with the confirmation the service answers (409 here: nothing to act on).
func TestIdentityRoutesRefuseWithoutConfirmation(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "etc/config/netconfig"), []byte("HOSTNAME=lite-test\n"), 0o644)
	root := system.Root(dir)
	srv := logDownloadServer(t, &SystemAPI{Root: root, HmIPLocalKey: &system.HmIPLocalKey{Root: root, StateDir: filepath.Join(dir, "state")}})
	const p = "/api/system/v1/radio/hmip"
	for _, tc := range []struct {
		name, method, path, body string
		st                       int
		err                      string
	}{
		// local key mode on: confirm; the generated key (every device taught in again) the name typed
		{"on, no confirm", "PUT", p + "/local-key", `{"mode":"known","network_key":"00112233445566778899AABBCCDDEEFF"}`, 400, "confirm"},
		{"on, known, confirmed", "PUT", p + "/local-key", `{"mode":"known","network_key":"00112233445566778899AABBCCDDEEFF","confirm":true}`, 409, "local-key"},
		{"on, generate, no confirm", "PUT", p + "/local-key", `{"mode":"generate","hostname":"lite-test"}`, 400, "confirm"},
		{"on, generate, no name", "PUT", p + "/local-key", `{"mode":"generate","confirm":true}`, 400, "hostname"},
		{"on, generate, another name", "PUT", p + "/local-key", `{"mode":"generate","confirm":true,"hostname":"other"}`, 400, "hostname"},
		{"on, generate, confirmed", "PUT", p + "/local-key", `{"mode":"generate","confirm":true,"hostname":"LITE-TEST"}`, 409, "local-key"},
		// local key mode off: the snapshot's files replace the ones in use - the name typed
		{"off, no body", "DELETE", p + "/local-key", ``, 422, "invalid-body"},
		{"off, no confirm", "DELETE", p + "/local-key", `{"hostname":"lite-test"}`, 400, "confirm"},
		{"off, another name", "DELETE", p + "/local-key", `{"confirm":true,"hostname":"other"}`, 400, "hostname"},
		{"off, confirmed", "DELETE", p + "/local-key", `{"confirm":true,"hostname":"lite-test"}`, 409, "local-key"},
		// the fresh start and the way back after a module move: the name typed
		{"fresh start, no confirm", "POST", p + "/exchange/fresh-start", `{"hostname":"lite-test","local_key":true}`, 400, "confirm"},
		{"fresh start, another name", "POST", p + "/exchange/fresh-start", `{"confirm":true,"hostname":"other"}`, 400, "hostname"},
		{"fresh start, confirmed", "POST", p + "/exchange/fresh-start", `{"confirm":true,"hostname":"lite-test"}`, 409, "local-key"},
		{"move back, no confirm", "POST", p + "/module-move/back", `{"hostname":"lite-test"}`, 400, "confirm"},
		{"move back, another name", "POST", p + "/module-move/back", `{"confirm":true,"hostname":"other"}`, 400, "hostname"},
		{"move back, confirmed", "POST", p + "/module-move/back", `{"confirm":true,"hostname":"lite-test"}`, 409, "local-key"},
		// the restore of a fresh-start snapshot: the name typed (task 212)
		{"restore, no confirm", "POST", p + "/local-key/snapshots/3014F711A0001F0000000A04/restore", `{"hostname":"lite-test"}`, 400, "confirm"},
		{"restore, another name", "POST", p + "/local-key/snapshots/3014F711A0001F0000000A04/restore", `{"confirm":true,"hostname":"other"}`, 400, "hostname"},
		// a whole backup's restore replaces /usr/local, the identity files with it: confirm
		{"backup restore, no confirm", "POST", "/api/system/v1/restore/apply", `{"file":"restore-1.sbk"}`, 400, "confirm"},
	} {
		st, out, body := do(t, srv, tc.method, tc.path, tc.body, nil)
		if st != tc.st || out["error"] != tc.err {
			t.Errorf("%s: %d %s", tc.name, st, body)
		}
	}
	// nothing was written: no state, no snapshot, no copy of the identity files
	if _, err := os.Stat(filepath.Join(dir, "state")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(dir, "state"))
		for _, e := range entries {
			if e.Name() != "state.json" {
				t.Errorf("a refused request left %s", e.Name())
			}
		}
	}
}
