package sshkeys

import (
	"strings"
	"testing"
)

// real keys from ssh-keygen, with the fingerprints ssh-keygen -l printed for them
const (
	ed25519Key  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEFSx3g/oLwaC84xZMOZIv9tk8m/eImdz0UxBwEjrnMj laptop ed"
	rsa2048Key  = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCnrcaxNeTE/9gzb6sMHDkRJBBMAa+WTrkVeA8PGZPGAlQ4Uk7V4S07fUlNENR6z6Gze76vjU2xmu1zXEGlZC7YQfCRkfX2sLXBKHtAQTHk2q0L8SyPswmdQ2nU0N20fX3AcVruws0m5mp+LQXSL9GpMwQ7YL/4AW6x9I4VlxvpDFsyY1xiGziMSAvtG2MckovQmPL5SEP9JtnEjMOdNbaoEQbI98NV+6hWN/tCu5orJZkvl+IZV6FwH/Q+SB5O9IGNJEdKdgIlptEGxN8Ahe6UUlIRDnYQg7AXVzsJlf+c4mZHn2SBz9BYcMbcai8+6ic7d/VKKEuPfgVRAwoHhj/5 rsa2048"
	rsa3072Key  = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCOvN1TaNOxNdQnBUpQMsOrfT0RjylvbY+YKOMTo5UoHOyscnMCADCShnMc4fOdwCcSkzTGpv++PiyVgxgzlBqks7DQp1AG9thxFEXwKpPGv0s4fOrTFLBaUGY59vz6nrEUBMt+OHk53ED8jcBoN7prgte56e8bgIcDHgFJKgFLOW0c5zEZjX3Y2kuRywCOVD3aDccmQCuT15fTaBHb44f/dx2JL7nQQbkcKKoYg9gsq2tw7mugsh9f0SbpJICbh8eRggBVV1RMPEhRy/QEAgb4CtrzOuNXlt2FFb48u0vRjGrYlY+hs5BQuM05N/2jMB0QvS7lO4fSEBEvQvd94RpRqANdnsgQBnAf0ZpjecIh8BisjprwNzQ4ey3/YFAywZWoTFUAxwnCnU9yi5MUM/+eW7to/ESsWMBN/wft5IxsnRm9JX3qgW6et8LWReA14wku/ibt1zBAsc2WcgKAVIMCA54i+U8dVJqcr2dz3kEgBMWcJdUL4JlVZlcomKaNCeM= rsa3072"
	ecdsa384Key = "ecdsa-sha2-nistp384 AAAAE2VjZHNhLXNoYTItbmlzdHAzODQAAAAIbmlzdHAzODQAAABhBPUSfBJXk7J2BPZi3b7iRP74eM1MdcEXFedN6chSVTyF/9aDxY1p7bBOHauaCNJAr8v/NTm3IalSCRmjTG/Npl7TxfRK0yehzV9v+hQe5qbKdfpLnVSd9f3J/45iXcD/xg== ec384"
)

func TestParseModernKeys(t *testing.T) {
	for _, c := range []struct {
		line, typ, fp, comment string
		bits                   int
	}{
		{ed25519Key, "ssh-ed25519", "SHA256:mZ+3k7WSTBEIONtZ4sWyKZdRbsGKX4yQvqbX/7pwwqs", "laptop ed", 256},
		{rsa3072Key, "ssh-rsa", "SHA256:VXmjnbqcbbwG4d05z/qZjvu9b3Gefb8hyFHVHSCDw1Y", "rsa3072", 3072},
		{ecdsa384Key, "ecdsa-sha2-nistp384", "SHA256:JPKsqnUXPohkbwQb+p8EaDAvyHUVqk93XQ0m5tVy6JA", "ec384", 384},
	} {
		k, err := Parse("  " + c.line + "\n")
		if err != nil {
			t.Fatalf("%s: %v", c.typ, err)
		}
		if k.Type != c.typ || k.Fingerprint != c.fp || k.Comment != c.comment || k.Bits != c.bits {
			t.Errorf("%s: %+v", c.typ, k)
		}
		if k.Line() != c.line {
			t.Errorf("%s: the line %q", c.typ, k.Line())
		}
	}
}

func TestParseRefuses(t *testing.T) {
	parts := strings.Fields(ed25519Key)
	for name, line := range map[string]string{
		"empty":                      "   ",
		"a short RSA key":            rsa2048Key,
		"an options prefix":          `from="10.0.0.1" ` + ed25519Key,
		"two keys":                   ed25519Key + "\n" + rsa3072Key,
		"DSA":                        "ssh-dss AAAAB3NzaC1kc3M= old",
		"a private key":              "-----BEGIN OPENSSH PRIVATE KEY-----",
		"no key after the type":      "ssh-ed25519",
		"not base64":                 "ssh-ed25519 !!!! x",
		"the wrong type in the data": "ssh-rsa " + parts[1],
		"a cut key":                  "ssh-ed25519 " + parts[1][:20],
	} {
		if _, err := Parse(line); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
}

func TestReadAndWriteTheSection(t *testing.T) {
	ed, _ := Parse(ed25519Key)
	rsa, _ := Parse(rsa3072Key)
	lab := `command="echo hi" ` + rsa2048Key
	file := "# the lab's key\n" + lab + "\n" + ecdsa384Key + "\n"

	// the section goes at the end; everything before it stays byte for byte
	out := WithSection([]byte(file), []Key{ed, rsa})
	want := file + "\n" + Begin + "\n" + ed25519Key + "\n" + rsa3072Key + "\n" + End + "\n"
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	f := Read(out)
	if len(f.Managed) != 2 || f.Managed[1].Fingerprint != rsa.Fingerprint {
		t.Errorf("managed %+v", f.Managed)
	}
	if len(f.Other) != 2 || f.Other[0].Options != `command="echo hi"` || f.Other[0].Bits != 2048 || f.Other[1].Type != "ecdsa-sha2-nistp384" {
		t.Errorf("other %+v", f.Other)
	}

	// a second write replaces the section, it does not add one
	out2 := WithSection(out, []Key{rsa})
	if strings.Count(string(out2), Begin) != 1 || strings.Contains(string(out2), ed25519Key) || !strings.HasPrefix(string(out2), file) {
		t.Errorf("rewritten:\n%s", out2)
	}
	// no keys: no section, and the rest as it was
	if got := string(WithSection(out2, nil)); got != file {
		t.Errorf("emptied:\n%q", got)
	}
	// a file of the section alone, and no file at all
	if got := string(WithSection(nil, []Key{ed})); got != Begin+"\n"+ed25519Key+"\n"+End+"\n" {
		t.Errorf("new file %q", got)
	}
	// a file without its final newline keeps its last line whole
	if got := string(WithSection([]byte(ecdsa384Key), []Key{ed})); !strings.HasPrefix(got, ecdsa384Key+"\n\n"+Begin) {
		t.Errorf("no final newline: %q", got)
	}
}

func TestSectionLineThatIsNoKey(t *testing.T) {
	f := Read([]byte(Begin + "\nnot a key\n" + ed25519Key + "\n" + End + "\n"))
	if len(f.Managed) != 1 || len(f.Other) != 0 {
		t.Errorf("%+v", f)
	}
}
