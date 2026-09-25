package system

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/hobbyquaker/occulited/internal/manifest"
)

// manifestFor builds a manifest for id whose runtime block is rt as the policy would store it -
// the tests' way of saying "the addon declares this".
func manifestFor(id string, rt *AddonRuntime) *manifest.Manifest {
	m := &manifest.Manifest{Format: manifest.Format, ID: id, Name: manifest.Text{"en": id}}
	if rt == nil {
		return m
	}
	m.Runtime = &manifest.Runtime{Root: rt.Root, Capabilities: rt.Capabilities, Groups: rt.Groups, Paths: rt.Paths, DataDirs: rt.DataDirs, Ports: rt.Ports, Start: rt.Start, APIScopes: rt.APIScopes}
	if rt.Needs != nil {
		ids := append([]string{}, (*rt.Needs)...)
		m.Runtime.Needs = &ids
	}
	for k, v := range rt.PortInfo {
		if m.Runtime.PortInfo == nil {
			m.Runtime.PortInfo = map[string]manifest.PortInfo{}
		}
		m.Runtime.PortInfo[k] = manifest.PortInfo{Proto: v.Proto, TLS: v.TLS, Label: manifest.Text(v.Label)}
	}
	if rt.SettingsURL != "" {
		m.UI.SettingsURL = rt.SettingsURL
	}
	return m
}

// writeManifest stores a manifest for id beside the policies, as an install would.
func writeManifest(t *testing.T, r Root, id string, rt *AddonRuntime) {
	t.Helper()
	if err := os.MkdirAll(r.join(AddonPolicyDir), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(manifestFor(id, rt))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.join(AddonPolicyDir+"/"+id+AddonManifestSuffix), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
