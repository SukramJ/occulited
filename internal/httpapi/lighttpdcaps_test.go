package httpapi

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// openccu-lite B-239: deploy/lighttpd/occulited.conf caps every request body in front of occulited at
// 8 MiB except on the upload routes, which keep lighttpd's global ceiling. The exception is a regular
// expression in the fragment; this keeps it and the route table together: every route that takes an
// upload matches it, no other route with a body under /api/ does, and every route it names exists.
func TestLighttpdUploadRoutes(t *testing.T) {
	fullAPI(t)
	conf, err := os.ReadFile("../../deploy/lighttpd/occulited.conf")
	if err != nil {
		t.Fatal(err)
	}
	var pattern string
	for _, line := range strings.Split(string(conf), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `$HTTP["url"] !~ "^/api/`) {
			pattern = strings.TrimSuffix(strings.TrimPrefix(line, `$HTTP["url"] !~ "`), `" {`)
		}
	}
	if pattern == "" {
		t.Fatal("no exception to the 8 MiB cap under /api/ in occulited.conf")
	}
	exempt, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("the exception is not a regular expression Go reads too: %v", err)
	}
	uploads := map[string]bool{
		"POST /api/system/v1/addons/install":        true, // an addon archive, MaxAddonSize
		"POST /api/system/v1/radio/firmware/upload": true, // a coprocessor firmware file
		"POST /api/system/v1/firmware/upload":       true, // a device firmware bundle, 64 MiB
		"POST /api/system/v1/restore/check":         true, // a backup, 2 GiB
		"POST /api/system/v1/system-update/upload":  true, // a system image, 2 GiB
		"POST /api/meta/v1/import/regadom":          true, // a ReGa database, 64 MiB
	}
	table := RouteScopes()
	for p := range uploads {
		if _, ok := table[p]; !ok {
			t.Errorf("the upload route %q is not in the route table any more: rename it in occulited.conf too", p)
		}
	}
	for p := range table {
		method, path, _ := strings.Cut(p, " ")
		if !strings.HasPrefix(path, "/api/") {
			continue
		}
		if exempt.MatchString(path) != uploads[p] {
			if uploads[p] {
				t.Errorf("the upload route %q is capped at 8 MiB by occulited.conf", p)
			} else if method != "GET" && method != "DELETE" {
				t.Errorf("%q keeps lighttpd's global ceiling although it takes no upload", p)
			}
		}
	}
}
