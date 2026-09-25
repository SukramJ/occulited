package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// B-150: a missing file is a 404; a client-side route and the root still get the app shell
func TestMissingFileIsNotTheAppShell(t *testing.T) {
	h := Handler()
	for _, c := range []struct {
		path  string
		code  int
		shell bool
	}{
		{"/", 200, true},
		{"/system/interfaces", 200, true},
		{"/addons/catalog", 200, true},
		{"/addon-settings/hm2mqtt.js", 200, true},
		{"/catalog/ioBroker.json", 200, true},
		{"/config/img/devices/50/5_hm-cc-tc_thumb.png", 404, false},
		{"/assets/gone-1234.js", 404, false},
		{"/favicon-missing.ICO", 404, false},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		shell := strings.Contains(strings.ToLower(rec.Body.String()), "<!doctype html")
		if rec.Code != c.code || shell != c.shell {
			t.Errorf("%s: %d (shell %v), want %d (shell %v)", c.path, rec.Code, shell, c.code, c.shell)
		}
	}
}
