package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

// openccu-lite task 212: the restore of a fresh-start snapshot takes the fresh start's confirmation
// - confirm and the host name typed - before the service is asked; what the service refuses is 409.
func TestLocalKeyRestoreConfirmation(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "etc/config/netconfig"), []byte("HOSTNAME=lite-test\n"), 0o644)
	root := system.Root(dir)
	srv := logDownloadServer(t, &SystemAPI{Root: root, HmIPLocalKey: &system.HmIPLocalKey{Root: root, StateDir: filepath.Join(dir, "state")}})
	const path = "/api/system/v1/radio/hmip/local-key/snapshots/3014F711A0001F0000000A04/restore"
	for _, tc := range []struct {
		body string
		st   int
		err  string
	}{
		{`{"hostname":"lite-test"}`, 400, "confirm"},
		{`{"confirm":true,"hostname":"other"}`, 400, "hostname"},
		{`{"confirm":true,"hostname":" LITE-TEST "}`, 409, "local-key"}, // the service: no snapshot of it
	} {
		st, out, body := do(t, srv, "POST", path, tc.body, nil)
		if st != tc.st || out["error"] != tc.err {
			t.Errorf("%s: %d %s", tc.body, st, body)
		}
	}
	if st, out, body := do(t, srv, "POST", "/api/system/v1/radio/hmip/local-key/snapshots/nope/restore", `{"confirm":true,"hostname":"lite-test"}`, nil); st != 409 || out["message"] != "not an SGTIN" {
		t.Errorf("%d %s", st, body)
	}
}
