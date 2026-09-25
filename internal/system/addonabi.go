package system

import (
	"bytes"
	"debug/elf"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Binary compatibility of installed addons (task 25, maintainer 2026-09-07). An addon installed
// on a stock CCU3 or on another architecture carries binaries this box cannot execute: the
// switch to openccu-lite keeps /usr/local, so the addon comes along unchanged. The scan below
// walks the addon's files, reads the ELF header of everything that has one, and compares machine
// and interpreter with what this kernel can run. An addon whose binaries cannot run here is
// disabled exactly the way a ReGa-dependent one is - the rc.d script loses its executable bit,
// nothing of the addon is touched - and the Addons page says "disabled, needs update"; the repair
// is the catalogue's Install/Update button when the addon has a release for this architecture.
//
// The openccu-lite.ok marker does not exempt from this check: it says "runs without the ReGa",
// which is a statement about code, not about machine code.

// hostGOARCH is runtime.GOARCH; a variable so the tests can pretend to be another box.
var hostGOARCH = runtime.GOARCH

// nativeABI is what the box runs natively, by Go's name for its own architecture.
var nativeABI = map[string]elfABI{
	"amd64": {elf.EM_X86_64, elf.ELFCLASS64},
	"386":   {elf.EM_386, elf.ELFCLASS32},
	"arm64": {elf.EM_AARCH64, elf.ELFCLASS64},
	"arm":   {elf.EM_ARM, elf.ELFCLASS32},
}

// compatABI is the second personality a 64-bit kernel offers, and the loaders that prove the
// userland for it is installed. On aarch64 that is upstream's multilib32 package, which is why a
// stock CCU3's 32-bit addons survive the move to OpenCCU at all (D-43).
var compatABI = map[string]struct {
	abi     elfABI
	loaders []string
}{
	"amd64": {elfABI{elf.EM_386, elf.ELFCLASS32}, []string{"/lib/ld-linux.so.2", "/lib32/ld-linux.so.2"}},
	"arm64": {elfABI{elf.EM_ARM, elf.ELFCLASS32}, []string{"/lib/ld-linux-armhf.so.3", "/lib32/ld-linux-armhf.so.3", "/lib/ld-linux.so.3", "/lib32/ld-linux.so.3"}},
}

type elfABI struct {
	machine elf.Machine
	class   elf.Class
}

// machineNames are the ones a CCU addon can plausibly carry; anything else falls back to the
// constant's own name, which is still readable ("EM_MIPS").
var machineNames = map[elfABI]string{
	{elf.EM_X86_64, elf.ELFCLASS64}:  "x86-64",
	{elf.EM_386, elf.ELFCLASS32}:     "32-bit x86",
	{elf.EM_AARCH64, elf.ELFCLASS64}: "64-bit ARM (aarch64)",
	{elf.EM_ARM, elf.ELFCLASS32}:     "32-bit ARM",
}

func (a elfABI) String() string {
	if n, ok := machineNames[a]; ok {
		return n
	}
	bits := "32-bit"
	if a.class == elf.ELFCLASS64 {
		bits = "64-bit"
	}
	return bits + " " + strings.TrimPrefix(a.machine.String(), "EM_")
}

// RunnableABIs is what this box can execute: its native ABI, plus the 32-bit personality when
// that loader exists under the root.
func (r Root) RunnableABIs() []elfABI {
	out := []elfABI{}
	if n, ok := nativeABI[hostGOARCH]; ok {
		out = append(out, n)
	}
	if c, ok := compatABI[hostGOARCH]; ok {
		for _, l := range c.loaders {
			if st, err := os.Stat(r.join(l)); err == nil && !st.IsDir() {
				out = append(out, c.abi)
				break
			}
		}
	}
	return out
}

// HostABIDescription names what this box runs, for the reason string and the docs.
func (r Root) HostABIDescription() string {
	abis := r.RunnableABIs()
	names := make([]string, 0, len(abis))
	for _, a := range abis {
		names = append(names, a.String())
	}
	if len(names) == 0 {
		return "an architecture it cannot name"
	}
	return strings.Join(names, " and ")
}

// elfFacts is everything the check needs out of an ELF header.
type elfFacts struct {
	abi elfABI
	// interp is PT_INTERP: a program is loaded by it, a shared object (.so, a Node .node
	// module, a static binary) has none.
	interp string
}

var elfMagic = []byte{0x7f, 'E', 'L', 'F'}

// readELF reads the header of path when it is an ELF file. The first four bytes decide, so a
// tree of thousands of scripts costs one short read each.
func readELF(path string) (elfFacts, bool) {
	f, err := os.Open(path)
	if err != nil {
		return elfFacts{}, false
	}
	defer f.Close()
	var head [4]byte
	if n, _ := f.ReadAt(head[:], 0); n != 4 || !bytes.Equal(head[:], elfMagic) {
		return elfFacts{}, false
	}
	ef, err := elf.NewFile(f)
	if err != nil {
		return elfFacts{}, false
	}
	facts := elfFacts{abi: elfABI{ef.Machine, ef.Class}}
	for _, p := range ef.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		// p_filesz comes straight out of the file and debug/elf does not validate it: clamp on the
		// unsigned value, because int() of a huge one is negative and make() then panics
		n := uint64(256)
		if p.Filesz < n {
			n = p.Filesz
		}
		b := make([]byte, n)
		if n, _ := p.ReadAt(b, 0); n > 0 {
			facts.interp = string(bytes.TrimRight(b[:n], "\x00"))
		}
		break
	}
	return facts, true
}

// runnable says whether this box can execute the file: the machine and word size must be one it
// runs, and a program's loader must actually exist. abis is r.RunnableABIs(), hoisted out of the
// walk so the loader stats happen once per scan rather than once per ELF file.
func (r Root) runnable(abis []elfABI, f elfFacts) bool {
	ok := false
	for _, a := range abis {
		if a == f.abi {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	if f.interp != "" {
		if _, err := os.Stat(r.join(f.interp)); err != nil {
			return false
		}
	}
	return true
}

type binVerdict struct {
	incompatible bool
	reason       string
}

var binScanCache sync.Map // id -> binVerdict

// maxABIScanFiles caps the walk. RedMatic's node_modules alone is tens of thousands of files and
// node_modules is exactly where the native modules are, so it is not skipped the way the ReGa
// scan skips it - but the walk stops rather than grow without bound.
const maxABIScanFiles = 60000

// BinaryCompatibility says whether an installed addon carries binaries this box cannot run, and
// which. The verdict is deliberately conservative, so that a package shipping binaries for
// several architectures beside each other is not punished for the ones it does not use:
//
//   - if the addon has ELF programs (files with a loader) and none of them runs here, it is
//     incompatible - the thing that would be started cannot start;
//   - otherwise, if it has only shared objects (.so, Node .node modules, static binaries) and
//     none of those runs here, it is incompatible - nothing of it can be loaded;
//   - an addon with no ELF file at all, or with at least one runnable program, is fine.
//
// Scanned once per process, like the ReGa scan.
func (r Root) BinaryCompatibility(id string) (bool, string) {
	if v, ok := binScanCache.Load(id); ok {
		vv := v.(binVerdict)
		return vv.incompatible, vv.reason
	}
	v := r.scanBinaries(id)
	binScanCache.Store(id, v)
	return v.incompatible, v.reason
}

func (r Root) scanBinaries(id string) binVerdict {
	var (
		files                 int
		truncated             bool
		programs, objects     int
		okPrograms, okObjects int
		badProgs, badObjs     []string
		missingLoader         []string
		seenBad               = map[elfABI]bool{}
		badABIs               []elfABI
	)
	abis := r.RunnableABIs()
	note := func(path string, f elfFacts, list *[]string) {
		rel := path
		if root := string(r); root != "" && root != "/" {
			rel = strings.TrimPrefix(path, root)
		}
		if !strings.HasPrefix(rel, "/") {
			rel = "/" + rel
		}
		if len(*list) < 3 {
			*list = append(*list, rel)
		}
		if !seenBad[f.abi] {
			seenBad[f.abi] = true
			badABIs = append(badABIs, f.abi)
		}
	}
	for _, dir := range []string{"/usr/local/addons/" + id, AddonWWW + "/" + id} {
		_ = filepath.WalkDir(r.join(dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			// symlinks are not followed: one inside the addon points at a file the walk reaches
			// anyway, and one pointing out of it is not the addon's own binary. The direction of
			// that error is under-detection, never a false "incompatible".
			if !d.Type().IsRegular() {
				return nil
			}
			files++
			if files > maxABIScanFiles {
				truncated = true
				return filepath.SkipAll
			}
			if st, err := d.Info(); err != nil || st.Size() < 52 { // an ELF32 header is 52 bytes
				return nil
			}
			f, ok := readELF(path)
			if !ok {
				return nil
			}
			runs := r.runnable(abis, f)
			switch {
			case f.interp != "":
				programs++
				if runs {
					okPrograms++
					return filepath.SkipAll // one runnable program settles it
				}
				note(path, f, &badProgs)
				if _, err := os.Stat(r.join(f.interp)); err != nil && len(missingLoader) < 3 {
					missingLoader = append(missingLoader, f.interp)
				}
			default:
				objects++
				if runs {
					okObjects++
					return nil
				}
				note(path, f, &badObjs)
			}
			return nil
		})
		if okPrograms > 0 {
			break
		}
	}
	switch {
	case okPrograms > 0:
		return binVerdict{}
	case truncated:
		// the walk stopped early, so "nothing runnable was found" means "not yet": a verdict from
		// a partial scan would disable a working addon, which is the expensive mistake here
		return binVerdict{}
	case programs > 0:
		return binVerdict{true, binReason(r, badProgs, badABIs, missingLoader)}
	case objects > 0 && okObjects == 0:
		return binVerdict{true, binReason(r, badObjs, badABIs, missingLoader)}
	}
	return binVerdict{}
}

func binReason(r Root, where []string, abis []elfABI, missingLoader []string) string {
	names := make([]string, 0, len(abis))
	for _, a := range abis {
		names = append(names, a.String())
	}
	sort.Strings(names)
	sort.Strings(missingLoader)
	msg := fmt.Sprintf("its binaries are %s and this system runs %s: %s",
		strings.Join(names, " and "), r.HostABIDescription(), strings.Join(where, ", "))
	if len(missingLoader) > 0 {
		msg += fmt.Sprintf("; the loader %s is not on this system", strings.Join(missingLoader, ", "))
	}
	return msg
}

// ForgetAddonScans drops the cached ReGa and binary verdicts. Both scans run once per process,
// and an install, an update or an uninstall replaces exactly what they looked at - the addon's
// code and its binaries - so a reinstalled addon has to be judged again. Without this, the
// "disabled, needs update" badge that sent the user to the catalogue would still be there after
// the catalogue fixed it, until occulited restarted. An empty id forgets everything, which is
// what an install has to do: the archive names the addon, not the caller.
func ForgetAddonScans(id string) {
	if id == "" {
		regaScanCache.Clear()
		binScanCache.Clear()
		return
	}
	regaScanCache.Delete(id)
	binScanCache.Delete(id)
}

// DisableIncompatibleBinaryAddons is the first-boot step beside DisableRegaDependentAddons: an
// addon whose binaries this box cannot run is disabled and shown as "disabled, needs update".
// Returns the ids it disabled.
func (r Root) DisableIncompatibleBinaryAddons() []string {
	var out []string
	entries, _ := os.ReadDir(r.join("/usr/local/etc/config/rc.d"))
	for _, e := range entries {
		id := e.Name()
		if e.IsDir() || !addonIDRe.MatchString(id) || !r.AddonEnabled(id) {
			continue
		}
		if bad, _ := r.BinaryCompatibility(id); bad {
			if r.SetAddonEnabled(id, false) == nil {
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}
