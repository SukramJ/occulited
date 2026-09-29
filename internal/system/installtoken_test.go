package system

import (
	"os"
	"testing"
)

// openccu-lite B-274: the install token is a fresh secret at every start, root's alone.
func TestWriteInstallToken(t *testing.T) {
	r := rootWith(t, map[string]string{"run/.keep": ""})
	fp := &chownPriv{}
	old := Priv
	Priv = fp
	t.Cleanup(func() { Priv = old })
	a, err := WriteInstallToken(r)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(r.join(InstallTokenFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", st, err)
	}
	if got := readFile(r.join(InstallTokenFile)); got != a+"\n" || len(a) != 48 {
		t.Fatalf("content %q for %q", got, a)
	}
	b, err := WriteInstallToken(r)
	if err != nil || b == a || readFile(r.join(InstallTokenFile)) != b+"\n" {
		t.Fatalf("a second start: %q %v", b, err)
	}
	if len(fp.chowns) != 2 || fp.chowns[0] != "install-token:0" {
		t.Fatalf("owner: %v", fp.chowns)
	}
}
