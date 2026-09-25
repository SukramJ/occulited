package system

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeELF writes the smallest file debug/elf accepts: an ELF header, and one PT_INTERP program
// header when interp is not empty. Enough for a machine/class/interpreter check, and it keeps the
// test independent of any cross toolchain.
func fakeELF(t *testing.T, path string, class elf.Class, machine elf.Machine, interp string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	var ident [16]byte
	copy(ident[:], "\x7fELF")
	ident[4] = byte(class)
	ident[5] = byte(elf.ELFDATA2LSB)
	ident[6] = byte(elf.EV_CURRENT)
	var buf []byte
	phnum := uint16(0)
	if interp != "" {
		phnum = 1
	}
	if class == elf.ELFCLASS64 {
		const ehsize, phentsize = 64, 56
		h := make([]byte, ehsize)
		copy(h, ident[:])
		le.PutUint16(h[16:], uint16(elf.ET_DYN))
		le.PutUint16(h[18:], uint16(machine))
		le.PutUint32(h[20:], uint32(elf.EV_CURRENT))
		if phnum > 0 {
			le.PutUint64(h[32:], ehsize) // e_phoff
		}
		le.PutUint16(h[52:], ehsize)
		le.PutUint16(h[54:], phentsize)
		le.PutUint16(h[56:], phnum)
		buf = h
		if phnum > 0 {
			p := make([]byte, phentsize)
			le.PutUint32(p[0:], uint32(elf.PT_INTERP))
			le.PutUint64(p[8:], ehsize+phentsize) // p_offset
			le.PutUint64(p[32:], uint64(len(interp)+1))
			le.PutUint64(p[40:], uint64(len(interp)+1))
			buf = append(buf, p...)
		}
	} else {
		const ehsize, phentsize = 52, 32
		h := make([]byte, ehsize)
		copy(h, ident[:])
		le.PutUint16(h[16:], uint16(elf.ET_DYN))
		le.PutUint16(h[18:], uint16(machine))
		le.PutUint32(h[20:], uint32(elf.EV_CURRENT))
		if phnum > 0 {
			le.PutUint32(h[28:], ehsize) // e_phoff
		}
		le.PutUint16(h[40:], ehsize)
		le.PutUint16(h[42:], phentsize)
		le.PutUint16(h[44:], phnum)
		buf = h
		if phnum > 0 {
			p := make([]byte, phentsize)
			le.PutUint32(p[0:], uint32(elf.PT_INTERP))
			le.PutUint32(p[4:], ehsize+phentsize) // p_offset
			le.PutUint32(p[16:], uint32(len(interp)+1))
			le.PutUint32(p[20:], uint32(len(interp)+1))
			buf = append(buf, p...)
		}
	}
	if interp != "" {
		buf = append(buf, append([]byte(interp), 0)...)
	}
	for len(buf) < 64 { // above the scan's minimum size
		buf = append(buf, 0)
	}
	if err := os.WriteFile(path, buf, 0o755); err != nil {
		t.Fatal(err)
	}
}

func withArch(t *testing.T, goarch string) {
	t.Helper()
	old := hostGOARCH
	hostGOARCH = goarch
	t.Cleanup(func() { hostGOARCH = old })
}

func forgetBinScan(ids ...string) {
	for _, id := range ids {
		binScanCache.Delete(id)
	}
}

func TestBinaryCompatibilityOnX86(t *testing.T) {
	withArch(t, "amd64")
	r := rootWith(t, map[string]string{
		"usr/local/etc/config/rc.d/native":  "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/arm64":   "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/armhf":   "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/scripts": "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/mixed":   "#!/bin/sh\n",
		"usr/local/addons/scripts/run.tcl":  "puts hi\n",
		"lib/ld-linux-x86-64.so.2":          "loader\n",
	})
	fakeELF(t, r.join("/usr/local/addons/native/bin/tool"), elf.ELFCLASS64, elf.EM_X86_64, "/lib/ld-linux-x86-64.so.2")
	fakeELF(t, r.join("/usr/local/addons/arm64/bin/tool"), elf.ELFCLASS64, elf.EM_AARCH64, "/lib/ld-linux-aarch64.so.1")
	fakeELF(t, r.join("/usr/local/addons/armhf/bin/tool"), elf.ELFCLASS32, elf.EM_ARM, "/lib/ld-linux-armhf.so.3")
	// a package that ships prebuilt native modules for several architectures beside each other
	fakeELF(t, r.join("/usr/local/addons/mixed/prebuilds/linux-arm64/x.node"), elf.ELFCLASS64, elf.EM_AARCH64, "")
	fakeELF(t, r.join("/usr/local/addons/mixed/prebuilds/linux-x64/x.node"), elf.ELFCLASS64, elf.EM_X86_64, "")

	for _, id := range []string{"native", "scripts", "mixed"} {
		if bad, why := r.BinaryCompatibility(id); bad {
			t.Errorf("%s flagged: %s", id, why)
		}
	}
	// the box is x86_64: neither ARM addon can run, and neither loader is there
	if bad, why := r.BinaryCompatibility("arm64"); !bad || !strings.Contains(why, "64-bit ARM") || !strings.Contains(why, "x86-64") || !strings.Contains(why, "bin/tool") {
		t.Errorf("arm64: %v %q", bad, why)
	}
	if bad, why := r.BinaryCompatibility("armhf"); !bad || !strings.Contains(why, "32-bit ARM") || !strings.Contains(why, "ld-linux-armhf.so.3") {
		t.Errorf("armhf: %v %q", bad, why)
	}
}

func TestBinaryCompatibilityOnAarch64(t *testing.T) {
	withArch(t, "arm64")
	files := map[string]string{
		"usr/local/etc/config/rc.d/armhf": "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/x86":   "#!/bin/sh\n",
	}
	r := rootWith(t, files)
	fakeELF(t, r.join("/usr/local/addons/armhf/bin/tool"), elf.ELFCLASS32, elf.EM_ARM, "/lib/ld-linux-armhf.so.3")
	fakeELF(t, r.join("/usr/local/addons/x86/bin/tool"), elf.ELFCLASS64, elf.EM_X86_64, "/lib/ld-linux-x86-64.so.2")
	// the cache is keyed by addon id alone, and the x86 test above used the same ids
	forgetBinScan("armhf", "x86")

	// without multilib32 the 32-bit ARM addon does not run either
	if bad, _ := r.BinaryCompatibility("armhf"); !bad {
		t.Error("armhf accepted without a 32-bit loader")
	}
	// upstream's multilib32 puts the loader under /lib32 and links it into /lib
	forgetBinScan("armhf")
	for _, d := range []string{"/lib32", "/lib"} {
		if err := os.MkdirAll(r.join(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(r.join("/lib32/ld-linux-armhf.so.3"), []byte("loader\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.join("/lib/ld-linux-armhf.so.3"), []byte("loader\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if bad, why := r.BinaryCompatibility("armhf"); bad {
		t.Errorf("armhf still flagged with /lib32 present: %s", why)
	}
	if bad, why := r.BinaryCompatibility("x86"); !bad || !strings.Contains(why, "x86-64") {
		t.Errorf("x86: %v %q", bad, why)
	}
	if d := r.HostABIDescription(); !strings.Contains(d, "64-bit ARM") || !strings.Contains(d, "32-bit ARM") {
		t.Errorf("host description %q", d)
	}
}

// a program whose loader is missing is incompatible even when the machine matches: that is the
// shape B-12 had on the systemd products, and what a stock CCU3's 32-bit addon hits here.
func TestBinaryCompatibilityDanglingLoader(t *testing.T) {
	withArch(t, "amd64")
	r := rootWith(t, map[string]string{"usr/local/etc/config/rc.d/old": "#!/bin/sh\n"})
	fakeELF(t, r.join("/usr/local/addons/old/bin/tool"), elf.ELFCLASS64, elf.EM_X86_64, "/lib/ld-uclibc.so.0")
	if bad, why := r.BinaryCompatibility("old"); !bad || !strings.Contains(why, "ld-uclibc.so.0") {
		t.Errorf("%v %q", bad, why)
	}
}

// A reinstall replaces the binaries the scan judged, so the verdict must not outlive it.
func TestForgetAddonScans(t *testing.T) {
	withArch(t, "amd64")
	r := rootWith(t, map[string]string{"usr/local/etc/config/rc.d/fixme": "#!/bin/sh\n"})
	fakeELF(t, r.join("/usr/local/addons/fixme/bin/tool"), elf.ELFCLASS32, elf.EM_ARM, "")
	forgetBinScan("fixme")
	if bad, _ := r.BinaryCompatibility("fixme"); !bad {
		t.Fatal("the ARM build was not flagged")
	}
	// the catalogue installs the x86_64 release over it
	fakeELF(t, r.join("/usr/local/addons/fixme/bin/tool"), elf.ELFCLASS64, elf.EM_X86_64, "")
	if bad, _ := r.BinaryCompatibility("fixme"); !bad {
		t.Error("the cached verdict was expected to survive until it is forgotten")
	}
	ForgetAddonScans("")
	if bad, why := r.BinaryCompatibility("fixme"); bad {
		t.Errorf("still flagged after the reinstall: %s", why)
	}
	// and the per-id form
	fakeELF(t, r.join("/usr/local/addons/fixme/bin/tool"), elf.ELFCLASS32, elf.EM_ARM, "")
	ForgetAddonScans("fixme")
	if bad, _ := r.BinaryCompatibility("fixme"); !bad {
		t.Error("not re-judged after ForgetAddonScans(id)")
	}
}

// A hostile addon must not be able to stop the daemon: p_filesz is copied out of the file and
// debug/elf does not validate it, so a huge one used to make the interpreter read allocate a
// negative length.
func TestBinaryCompatibilityHostileHeader(t *testing.T) {
	withArch(t, "amd64")
	r := rootWith(t, map[string]string{"usr/local/etc/config/rc.d/evil": "#!/bin/sh\n"})
	p := r.join("/usr/local/addons/evil/bin/tool")
	fakeELF(t, p, elf.ELFCLASS64, elf.EM_X86_64, "/lib/ld-linux-x86-64.so.2")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// p_filesz of the single PT_INTERP program header, at ehsize+32 for ELF64
	binary.LittleEndian.PutUint64(b[64+32:], 1<<63)
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	forgetBinScan("evil")
	// the only requirement is that this returns at all
	if bad, why := r.BinaryCompatibility("evil"); bad && why == "" {
		t.Error("a verdict with no reason")
	}
}

func TestDisableIncompatibleBinaryAddons(t *testing.T) {
	withArch(t, "amd64")
	r := rootWith(t, map[string]string{
		"usr/local/etc/config/rc.d/good": "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/bad":  "#!/bin/sh\n",
	})
	for _, id := range []string{"good", "bad"} {
		if err := os.Chmod(r.join("/usr/local/etc/config/rc.d/"+id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeELF(t, r.join("/usr/local/addons/good/bin/tool"), elf.ELFCLASS64, elf.EM_X86_64, "")
	fakeELF(t, r.join("/usr/local/addons/bad/bin/tool"), elf.ELFCLASS32, elf.EM_ARM, "")
	// the porting marker says "runs without the ReGa"; it says nothing about machine code
	if err := os.WriteFile(r.join("/usr/local/addons/bad/"+RegaCompatibleMarker), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	forgetBinScan("good", "bad")
	disabled := r.DisableIncompatibleBinaryAddons()
	if len(disabled) != 1 || disabled[0] != "bad" {
		t.Fatalf("%v", disabled)
	}
	if r.AddonEnabled("bad") || !r.AddonEnabled("good") {
		t.Error("enabled state")
	}
	if again := r.DisableIncompatibleBinaryAddons(); len(again) != 0 {
		t.Errorf("second run: %v", again)
	}
}
