package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openccu-lite task 307: the ingress scope's view of the addons - RedMatic's drop-in proxies
// /addons/red/, so "red" is redmatic's segment and redmatic the addon behind /addons/red/; Homematic
// Manager's drop-in proxies its own id and adds nothing; a segment nobody has belongs to no addon;
// the names come from the stored manifests without running a script, and hmm.script is no addon.
func TestAddonIngressSegments(t *testing.T) {
	root := Root(t.TempDir())
	w := func(p, c string, mode os.FileMode) {
		t.Helper()
		full := root.join(p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), mode); err != nil {
			t.Fatal(err)
		}
	}
	w("/usr/local/etc/config/rc.d/redmatic", "#!/bin/sh\n", 0o755)
	w("/usr/local/etc/config/rc.d/redmatic.script", "#!/bin/sh\n", 0o755)
	w("/usr/local/etc/config/rc.d/hmm", "#!/bin/sh\n", 0o755)
	w("/usr/local/etc/config/rc.d/mosquitto", "#!/bin/sh\n", 0o755)
	w("/usr/local/etc/config/lighttpd/redmatic.conf", `# RedMatic
url.redirect = ("^/addons/red$" => "/addons/red/")
$HTTP["url"] =~ "^/(addons/red/).*" {
  proxy.server = ("/addons/red/" => (( "host" => "127.0.0.1", "port" => 1880 )))
  proxy.header = ( "upgrade" => "enable" )
}
$HTTP["url"] =~ "^/(addons/redmatic-api/).*" {
  proxy.server = ( "" => (( "host" => "127.0.0.1", "port" => 1881 )))
}
$HTTP["url"] =~ "(^/description.xml)|(^/api/.*/lights)" {
  proxy.server = ( "" => (( "host" => "127.0.0.1", "port" => 80 )))
}
`, 0o644)
	w("/usr/local/etc/config/lighttpd/hmm.conf", `$HTTP["url"] =~ "^/addons/hmm/" {
  $HTTP["url"] !~ "^/addons/hmm/settings.cgi" {
    proxy.server = ("/addons/hmm/" => (( "host" => "127.0.0.1", "port" => 8090 )))
  }
}
`, 0o644)
	w(filepath.Join(AddonPolicyDir, "redmatic"+AddonManifestSuffix), `{"format": 1, "id": "redmatic", "name": "RedMatic"}`, 0o644)
	w(filepath.Join(AddonPolicyDir, "hmm"+AddonManifestSuffix), `{"format": 1, "id": "hmm", "name": "Homematic Manager"}`, 0o644)

	if got := strings.Join(root.AddonIngressSegments("redmatic"), ","); got != "red,redmatic-api" {
		t.Errorf("redmatic's segments: %q", got)
	}
	if got := root.AddonIngressSegments("hmm"); got != nil {
		t.Errorf("hmm's segments: %v (its drop-in proxies its own id)", got)
	}
	if got := root.AddonIngressSegments("mosquitto"); got != nil {
		t.Errorf("mosquitto's segments without a drop-in: %v", got)
	}
	if got := root.AddonIngressSegments("../etc"); got != nil {
		t.Errorf("a path as an id: %v", got)
	}
	for seg, want := range map[string]string{"red": "redmatic", "redmatic-api": "redmatic", "redmatic": "redmatic", "hmm": "hmm", "mosquitto": "mosquitto", "nope": "", "": "", "redmatic.script": ""} {
		if got := root.AddonForIngressSegment(seg); got != want {
			t.Errorf("the addon behind /addons/%s/: %q, want %q", seg, got, want)
		}
	}
	names := root.InstalledAddonNames()
	if names["redmatic"] != "RedMatic" || names["hmm"] != "Homematic Manager" || names["mosquitto"] == "" || len(names) != 3 {
		t.Errorf("names: %v", names)
	}
	// the menu's parser still takes the first path
	if p := frontendPath("proxy.server = (\"/addons/red/\" => ((\"host\" => \"127.0.0.1\")))\nproxy.server = (\"/addons/other/\" => ((\"host\" => \"127.0.0.1\")))"); p != "/addons/red/" {
		t.Errorf("frontendPath = %q", p)
	}
}
