package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// B-150: a missing file is a 404; a client-side route and the root still get the app shell.
//
// openccu-lite task 331: the system ships the WebUI's device pictures, DEVDB.tcl, stringtable_de.txt
// and translate.lang*.js at the CCU's paths, and lighttpd serves them itself (the fork's lite
// webui.conf), so those URLs never reach the shell. Should one reach it anyway - a lighttpd without
// that block, the development server - it is a 404 here, never the app shell in place of the file.
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
		{"/config/img/devices/250/coupling/c_1.png", 404, false},
		{"/config/devdescr/DEVDB.tcl", 404, false},
		{"/config/stringtable_de.txt", 404, false},
		{"/webui/js/lang/de/translate.lang.js", 404, false},
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

// openccu-lite task 259: every answer of the shell carries the policy of csp.txt - the page, the
// client-side routes, an asset, a 404 - and the policy has what the audit asked for.
func TestShellPolicy(t *testing.T) {
	csp, ok := Policy["Content-Security-Policy"]
	if !ok || Policy["Permissions-Policy"] == "" || len(Policy) != 2 {
		t.Fatalf("csp.txt: %v", Policy)
	}
	for _, want := range []string{"default-src 'self'", "script-src 'self';", "frame-ancestors 'self'", "object-src 'none'", "base-uri 'self'", "form-action 'self'", "connect-src 'self'", "frame-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("the policy lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(strings.SplitN(csp+";", "style-src", 2)[0], "unsafe-inline") {
		t.Errorf("scripts must never be inline or evaluated: %s", csp)
	}
	if !strings.Contains(Policy["Permissions-Policy"], "camera=(self)") || !strings.Contains(Policy["Permissions-Policy"], "microphone=()") {
		t.Errorf("Permissions-Policy: %s", Policy["Permissions-Policy"])
	}
	h := Handler()
	for _, p := range []string{"/", "/system/interfaces", "/addon-settings/x", "/app.webmanifest", "/assets/gone-1234.js", "/favicon-missing.ICO"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Header().Get("Content-Security-Policy") != csp || rec.Header().Get("Permissions-Policy") != Policy["Permissions-Policy"] {
			t.Errorf("%s (%d): headers %v", p, rec.Code, rec.Header())
		}
	}
}
