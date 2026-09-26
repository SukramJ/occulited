package ownwalk

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// The tests run as a normal user, who cannot give a file away. They use a fake ownership layer: the
// owner the walk sees comes from fakeOwners (the real one until the fake chown changed it), and the
// fake chown records the inode it was handed - through the descriptor, the way the real fchownat
// gets it - so what the walk would have changed is exactly the recorded set. Tests that need root
// say so in their name and skip otherwise (TestOwnAsRoot); the mount test runs as root of a user
// namespace where the host allows one (inNamespace).

const addonUID, addonGID = 30007, 30007

type fakeOwners struct {
	mu      sync.Mutex
	owner   map[[2]uint64][2]uint32
	chowned [][2]uint64
}

func newFake() *fakeOwners { return &fakeOwners{owner: map[[2]uint64][2]uint32{}} }

func (f *fakeOwners) hooks() *hooks {
	return &hooks{
		owner: func(st Stat) (uint32, uint32) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if o, ok := f.owner[[2]uint64{st.Dev, st.Ino}]; ok {
				return o[0], o[1]
			}
			return st.UID, st.GID
		},
		chown: func(fd, uid, gid int) error {
			st, err := statFD(fd)
			if err != nil {
				return err
			}
			if st.typ() == unix.S_IFLNK {
				return errors.New("the chown was handed a link")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			k := [2]uint64{st.Dev, st.Ino}
			f.owner[k] = [2]uint32{uint32(uid), uint32(gid)}
			f.chowned = append(f.chowned, k)
			return nil
		},
		chmod: func(fd int, mode uint32) error {
			// apply for real (the test user owns its own tree), so the tree settles and a re-walk sees it
			return fchmodFD(fd, mode)
		},
		protectedHardlinks: func() bool { return true },
	}
}

// inode is the inode a path names, the path itself when it is a link.
func inode(t *testing.T, path string) [2]uint64 {
	t.Helper()
	fd, err := unix.Open(path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	st, err := statFD(fd)
	if err != nil {
		t.Fatal(err)
	}
	return [2]uint64{st.Dev, st.Ino}
}

func (f *fakeOwners) set(t *testing.T, path string, uid, gid uint32) {
	t.Helper()
	k := inode(t, path)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owner[k] = [2]uint32{uid, gid}
}

func (f *fakeOwners) times(t *testing.T, path string) int {
	t.Helper()
	k := inode(t, path)
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.chowned {
		if c == k {
			n++
		}
	}
	return n
}

func (f *fakeOwners) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chowned = nil
}

// tree makes the entries below a temporary directory: a trailing "/" is a directory, anything
// else a file.
func tree(t *testing.T, entries ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, e := range entries {
		p := filepath.Join(root, e)
		if strings.HasSuffix(e, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func opts(f *fakeOwners) Options {
	return Options{UID: addonUID, GID: addonGID, hooks: f.hooks()}
}

// RedMatic's update copied var/ as root; bin/ was the addon's already. Exactly the wrong entries are
// changed - a wrong group alone is wrong too - and a second walk changes nothing.
func TestOwnFixesOnlyWrongOwners(t *testing.T) {
	root := tree(t, "addon/bin/node", "addon/var/start.lock/", "addon/var/flows.json", "addon/etc/settings.js")
	top := filepath.Join(root, "addon")
	if err := unix.Mkfifo(filepath.Join(top, "var/fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	right := []string{"", "bin", "bin/node", "etc"}
	for _, p := range right {
		f.set(t, filepath.Join(top, p), addonUID, addonGID)
	}
	f.set(t, filepath.Join(top, "etc/settings.js"), addonUID, 0)
	wrong := []string{"var", "var/start.lock", "var/flows.json", "var/fifo", "etc/settings.js"}

	res := Own([]string{top}, opts(f))
	if res.Checked != 9 || res.Wrong != len(wrong) || res.Fixed != len(wrong) || res.Problem() != "" || res.Left() != 0 {
		t.Fatalf("result %+v", res)
	}
	for _, p := range wrong {
		if n := f.times(t, filepath.Join(top, p)); n != 1 {
			t.Errorf("%s changed %d times, want once", p, n)
		}
	}
	for _, p := range right {
		if n := f.times(t, filepath.Join(top, p)); n != 0 {
			t.Errorf("%s had its owner already and was changed", p)
		}
	}
	if !strings.HasPrefix(res.FirstWrong, top+"/") {
		t.Errorf("first wrong entry %q", res.FirstWrong)
	}

	f.reset()
	again := Own([]string{top}, opts(f))
	if again.Wrong != 0 || again.Fixed != 0 || len(f.chowned) != 0 || again.Checked != 9 {
		t.Errorf("the second walk: %+v, %d chowns", again, len(f.chowned))
	}
}

func TestOwnDryRunChangesNothing(t *testing.T) {
	root := tree(t, "addon/var/a", "addon/var/b")
	f := newFake()
	o := opts(f)
	o.DryRun = true
	res := Own([]string{filepath.Join(root, "addon")}, o)
	if res.Wrong != 4 || res.Fixed != 0 || len(f.chowned) != 0 {
		t.Errorf("dry run: %+v, %d chowns", res, len(f.chowned))
	}
}

// B-123: the dry run says which wrong entries are root's - B-92's warning is about those - and a wrong
// group alone or another user is wrong but not root-owned.
func TestOwnDryRunCountsRootOwned(t *testing.T) {
	root := tree(t, "addon/var/db", "addon/var/lock", "addon/etc/conf", "addon/etc/other")
	top := filepath.Join(root, "addon")
	f := newFake()
	for _, p := range []string{"", "etc", "var/lock"} {
		f.set(t, filepath.Join(top, p), addonUID, addonGID)
	}
	f.set(t, filepath.Join(top, "var"), 0, 0)
	f.set(t, filepath.Join(top, "var/db"), 0, addonGID)
	f.set(t, filepath.Join(top, "etc/conf"), addonUID, 0) // a wrong group alone
	f.set(t, filepath.Join(top, "etc/other"), 1000, 1000) // another user
	o := opts(f)
	o.DryRun = true
	res := Own([]string{top}, o)
	if res.Checked != 7 || res.Wrong != 4 || res.RootOwned != 2 || res.FirstRootOwned != filepath.Join(top, "var") || res.Fixed != 0 || len(f.chowned) != 0 {
		t.Errorf("dry run: %+v, %d chowns", res, len(f.chowned))
	}
	f.set(t, filepath.Join(top, "var"), addonUID, addonGID)
	f.set(t, filepath.Join(top, "var/db"), addonUID, addonGID)
	if res := Own([]string{top}, o); res.RootOwned != 0 || res.FirstRootOwned != "" || res.Wrong != 2 {
		t.Errorf("nothing of root's left: %+v", res)
	}
}

// The addon's user plants links in var/ to root's files and directories outside: none is followed,
// no link is changed, and nothing outside is.
func TestOwnDoesNotFollowSymlinks(t *testing.T) {
	root := tree(t, "outside/shadow", "outside/dir/secret", "addon/var/x")
	top := filepath.Join(root, "addon")
	for link, target := range map[string]string{
		"var/shadow": filepath.Join(root, "outside/shadow"), // absolute, to a file
		"var/dir":    "../../outside/dir",                   // relative, to a directory
		"lib":        filepath.Join(root, "outside/dir"),    // a directory's link at the top
		"var/self":   top,                                   // a loop
	} {
		if err := os.Symlink(target, filepath.Join(top, link)); err != nil {
			t.Fatal(err)
		}
	}
	f := newFake()
	res := Own([]string{top}, opts(f))
	if res.Symlinks != 4 || res.Problem() != "" {
		t.Errorf("result %+v", res)
	}
	for _, p := range []string{"outside", "outside/shadow", "outside/dir", "outside/dir/secret", "addon/var/shadow", "addon/var/dir", "addon/lib", "addon/var/self"} {
		if f.times(t, filepath.Join(root, p)) != 0 {
			t.Errorf("%s was changed", p)
		}
	}
	if f.times(t, filepath.Join(top, "var/x")) != 1 {
		t.Error("the addon's own file was not changed")
	}
	if f.times(t, top) != 1 {
		t.Error("the top directory was changed more than once, or not at all")
	}
}

// The addon's user swaps a directory for a link to one of root's while the walk is at it: before
// the walk opens the name, between its open and its stat, and after its stat. The walk never gets
// outside; after the stat it walks the directory it checked, wherever that was moved to.
func TestOwnSwapRace(t *testing.T) {
	for _, at := range []string{"listed", "open", "stat"} {
		t.Run("at "+at, func(t *testing.T) {
			root := tree(t, "outside/dir/secret", "addon/var/cache/entry")
			top := filepath.Join(root, "addon")
			f := newFake()
			o := opts(f)
			swapAt := filepath.Join(top, "var/cache")
			if at == "listed" {
				swapAt = filepath.Join(top, "var")
			}
			var swapErr error
			swapped := false
			o.hooks.event = func(what, path string) {
				if what == at && path == swapAt && !swapped {
					swapped = true
					swapErr = errors.Join(
						os.Rename(filepath.Join(top, "var/cache"), filepath.Join(root, "moved")),
						os.Symlink(filepath.Join(root, "outside/dir"), filepath.Join(top, "var/cache")))
				}
			}
			res := Own([]string{top}, o)
			if !swapped || swapErr != nil {
				t.Fatalf("the swap did not happen: %v %v", swapped, swapErr)
			}
			for _, p := range []string{"outside/dir", "outside/dir/secret", "addon/var/cache"} {
				if f.times(t, filepath.Join(root, p)) != 0 {
					t.Errorf("%s was changed (%+v)", p, res)
				}
			}
			movedEntry := f.times(t, filepath.Join(root, "moved/entry"))
			switch at {
			case "stat":
				// the directory was checked before it moved: that inode is walked, not the link
				if movedEntry != 1 || f.times(t, filepath.Join(root, "moved")) != 1 || res.Symlinks != 0 {
					t.Errorf("after the stat the checked directory is walked: entry %d, %+v", movedEntry, res)
				}
			default:
				if movedEntry != 0 || res.Symlinks != 1 {
					t.Errorf("a link in its place is not followed: entry %d, %+v", movedEntry, res)
				}
			}
		})
	}
}

// The same race, for real: a goroutine swaps the directory and a link to root's directory back and
// forth while the walk runs again and again.
func TestOwnSwapRaceLoop(t *testing.T) {
	root := tree(t, "outside/dir/secret", "addon/var/d/entry", "addon/var/other")
	top := filepath.Join(root, "addon")
	f := newFake()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d, parked, outside := filepath.Join(top, "var/d"), filepath.Join(root, "parked"), filepath.Join(root, "outside/dir")
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Rename(d, parked)
			_ = os.Symlink(outside, d)
			_ = os.Remove(d)
			_ = os.Rename(parked, d)
		}
	}()
	for i := 0; i < 300; i++ {
		Own([]string{top}, opts(f))
	}
	close(stop)
	wg.Wait()
	for _, p := range []string{"outside", "outside/dir", "outside/dir/secret"} {
		if n := f.times(t, filepath.Join(root, p)); n != 0 {
			t.Errorf("%s was changed %d times", p, n)
		}
	}
}

// What is not safe to reach is refused, and what is no directory to walk is skipped; nothing in
// either is changed.
func TestOwnTopDirectories(t *testing.T) {
	root := tree(t, "open/addon/x", "sticky/addon/x", "usr/local/etc/config/addons/hmm/x", "usr/local/addons/hmm/www/x", "etc/", "file", "open/target/addon/x")
	for dir, mode := range map[string]os.FileMode{"open": 0o777, "sticky": 0o777 | os.ModeSticky} {
		if err := os.Chmod(filepath.Join(root, dir), mode); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		"etc/config":                          "../usr/local/etc/config",                       // the image's /etc/config: a trusted link with ..
		"usr/local/etc/config/addons/www/hmm": filepath.Join(root, "usr/local/addons/hmm/www"), // the image's www link
		"open/link":                           filepath.Join(root, "open/target"),              // a link in a directory anybody may change
	}
	for link, target := range links {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(root, link)), 0o755)
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	at := func(p string) string { return filepath.Join(root, p) }
	cases := []struct {
		name, dir string
		refused   string // a part of the reason
		skipped   string
		walked    string // a file the walk changes
	}{
		{name: "relative", dir: "usr/local/addons/hmm", refused: "not a clean absolute path"},
		{name: "unclean", dir: root + "/usr/local/addons/../addons/hmm", refused: "not a clean absolute path"},
		{name: "the root", dir: "/", refused: "not a clean absolute path"},
		{name: "below a directory anybody may write", dir: at("open/addon"), refused: "can be changed by a user other than root"},
		{name: "through a link in such a directory", dir: at("open/link/addon"), refused: "can be changed by a user other than root"},
		{name: "a file", dir: at("file/x"), refused: "is not a directory"},
		{name: "missing", dir: at("usr/local/addons/nothing"), skipped: "does not exist"},
		{name: "a link itself", dir: at("usr/local/etc/config/addons/www/hmm"), skipped: "a symbolic link, not followed"},
		{name: "below a sticky directory", dir: at("sticky/addon"), walked: "sticky/addon/x"},
		{name: "through a trusted link with ..", dir: at("etc/config/addons/hmm"), walked: "usr/local/etc/config/addons/hmm/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			res := Own([]string{tc.dir}, opts(f))
			switch {
			case tc.refused != "":
				if len(res.Refused) != 1 || !strings.Contains(res.Refused[0], tc.refused) || len(f.chowned) != 0 || res.Problem() == "" {
					t.Errorf("want refused (%s): %+v, %d chowns", tc.refused, res, len(f.chowned))
				}
			case tc.skipped != "":
				if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], tc.skipped) || len(f.chowned) != 0 || res.Problem() != "" {
					t.Errorf("want skipped (%s): %+v, %d chowns", tc.skipped, res, len(f.chowned))
				}
			default:
				if res.Problem() != "" || f.times(t, at(tc.walked)) != 1 {
					t.Errorf("want %s walked: %+v", tc.walked, res)
				}
			}
		})
	}
	// the target of the refused link stays as it was
	f := newFake()
	Own([]string{at("open/link/addon")}, opts(f))
	if f.times(t, at("open/target/addon/x")) != 0 {
		t.Error("a link in an open directory was followed")
	}
}

// A directory given twice, or one inside another given as well, is walked once.
func TestOwnWalksADirectoryOnce(t *testing.T) {
	root := tree(t, "addon/var/x")
	f := newFake()
	top := filepath.Join(root, "addon")
	Own([]string{top, filepath.Join(top, "var"), top}, opts(f))
	for _, p := range []string{"addon", "addon/var", "addon/var/x"} {
		if n := f.times(t, filepath.Join(root, p)); n != 1 {
			t.Errorf("%s changed %d times", p, n)
		}
	}
}

// A file with two links and another owner is changed only where the kernel keeps an addon's user
// from linking a file it could not write (fs.protected_hardlinks).
func TestOwnHardLinks(t *testing.T) {
	for _, protected := range []bool{false, true} {
		root := tree(t, "addon/var/a")
		top := filepath.Join(root, "addon")
		if err := os.Link(filepath.Join(top, "var/a"), filepath.Join(top, "var/b")); err != nil {
			t.Fatal(err)
		}
		f := newFake()
		f.set(t, top, addonUID, addonGID)
		f.set(t, filepath.Join(top, "var"), addonUID, addonGID)
		o := opts(f)
		o.hooks.protectedHardlinks = func() bool { return protected }
		res := Own([]string{top}, o)
		n := f.times(t, filepath.Join(top, "var/a"))
		if !protected && (n != 0 || res.HardLinks != 2 || res.Wrong != 2 || res.Left() != 2) {
			t.Errorf("unprotected: changed %d times, %+v", n, res)
		}
		if protected && (n != 1 || res.HardLinks != 0 || res.Fixed != 1 || res.Left() != 0) {
			t.Errorf("protected: changed %d times, %+v", n, res)
		}
	}
}

func TestOwnLimits(t *testing.T) {
	root := tree(t, "addon/a/b/c/d/e", "addon/f", "addon/g", "addon/h")
	top := filepath.Join(root, "addon")
	f := newFake()
	o := opts(f)
	o.MaxEntries = 3
	if res := Own([]string{top}, o); !res.Incomplete || !strings.Contains(res.Problem(), "stopped after 3 entries") {
		t.Errorf("entries: %+v", res)
	}
	f = newFake()
	o = opts(f)
	o.MaxDepth = 2
	res := Own([]string{top}, o)
	if res.TooDeep != 1 || f.times(t, filepath.Join(top, "a/b")) != 1 || f.times(t, filepath.Join(top, "a/b/c")) != 0 {
		t.Errorf("depth: %+v", res)
	}
}

// A name is the addon user's to choose: a newline in it must not make a line of its own in a message.
func TestOwnMessagesEscapeControlCharacters(t *testing.T) {
	root := tree(t, "addon/")
	top := filepath.Join(root, "addon")
	if err := os.WriteFile(filepath.Join(top, "x\nFAKE journal line"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	f.set(t, top, addonUID, addonGID)
	res := Own([]string{top}, opts(f))
	if strings.Contains(res.FirstWrong, "\n") || !strings.Contains(res.FirstWrong, `x\x0aFAKE`) {
		t.Errorf("first wrong %q", res.FirstWrong)
	}
}

// Another file system mounted below an addon's directory - a tmpfs, and a bind mount of a directory
// of the same file system - is neither changed nor entered. Needs root: it runs as root of a user
// and mount namespace where the host allows one, and skips otherwise.
func TestOwnStaysOnOneFileSystem(t *testing.T) {
	if os.Geteuid() != 0 {
		inNamespace(t, "TestOwnStaysOnOneFileSystem")
		return
	}
	root := tree(t, "addon/var/x", "addon/mnt/", "addon/bind/", "other/y")
	top := filepath.Join(root, "addon")
	if err := unix.Mount("tmpfs", filepath.Join(top, "mnt"), "tmpfs", 0, "size=1m"); err != nil {
		t.Skipf("no mount here: %v", err)
	}
	t.Cleanup(func() { _ = unix.Unmount(filepath.Join(top, "mnt"), unix.MNT_DETACH) })
	if err := os.WriteFile(filepath.Join(top, "mnt/inside"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount(filepath.Join(root, "other"), filepath.Join(top, "bind"), "", unix.MS_BIND, ""); err != nil {
		t.Fatalf("bind mount: %v", err)
	}
	t.Cleanup(func() { _ = unix.Unmount(filepath.Join(top, "bind"), unix.MNT_DETACH) })
	f := newFake()
	o := opts(f)
	// the host's root is the overflow uid in here, and owns "/" and the temporary directory's parents
	if b, err := os.ReadFile("/proc/sys/kernel/overflowuid"); err == nil {
		var overflow uint32
		for _, c := range strings.TrimSpace(string(b)) {
			overflow = overflow*10 + uint32(c-'0')
		}
		o.hooks.trusted = &overflow
	}
	res := Own([]string{top}, o)
	for _, p := range []string{"addon/mnt", "addon/mnt/inside"} {
		if f.times(t, filepath.Join(root, p)) != 0 {
			t.Errorf("%s on the tmpfs was changed", p)
		}
	}
	if f.times(t, filepath.Join(top, "var/x")) != 1 {
		t.Error("the addon's own file was not changed")
	}
	fd, _ := unix.Open(top, unix.O_PATH|unix.O_CLOEXEC, 0)
	st, _ := statFD(fd)
	unix.Close(fd)
	if st.HasMntID {
		if res.Mounts != 2 || f.times(t, filepath.Join(root, "other")) != 0 || f.times(t, filepath.Join(root, "other/y")) != 0 {
			t.Errorf("the bind mount of the same file system was entered: %+v", res)
		}
	} else if res.Mounts < 1 {
		t.Errorf("mounts %+v", res)
	}
}

// inNamespace runs the named test again as root of a new user and mount namespace (unshare -rm).
// There it may mount, though not give files away; it skips where the host allows no such namespace.
func inNamespace(t *testing.T, name string) {
	t.Helper()
	if os.Getenv("OWNWALK_NAMESPACE") != "" {
		t.Skip("not root in the namespace")
	}
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Skip("no unshare")
	}
	cmd := exec.Command(unshare, "-rm", os.Args[0], "-test.run=^"+name+"$", "-test.v")
	cmd.Env = append(os.Environ(), "OWNWALK_NAMESPACE=1")
	out, err := cmd.CombinedOutput()
	s := string(out)
	switch {
	case err != nil && !strings.Contains(s, "--- FAIL"):
		t.Skipf("no user namespace here: %v: %s", err, s)
	case err != nil:
		t.Fatalf("in the namespace:\n%s", s)
	case strings.Contains(s, "--- SKIP"):
		t.Skipf("skipped in the namespace:\n%s", s)
	case !strings.Contains(s, "--- PASS: "+name):
		t.Fatalf("no result from the namespace:\n%s", s)
	}
	t.Logf("ran as root of a user namespace")
}

// Root only: the real fchownat. A root-owned file, a planted link to root's file outside, a device
// node and an entry that is right already; afterwards only the wrong entries are the addon's.
func TestOwnAsRoot(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("OWNWALK_NAMESPACE") != "" {
		t.Skip("root only: gives files to uid 30007")
	}
	root := tree(t, "outside/shadow", "addon/var/x", "addon/var/right")
	top := filepath.Join(root, "addon")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Chown(filepath.Join(root, "outside/shadow"), 0, 0))
	must(os.Chown(filepath.Join(top, "var/right"), addonUID, addonGID))
	must(os.Symlink(filepath.Join(root, "outside/shadow"), filepath.Join(top, "var/shadow")))
	must(unix.Mknod(filepath.Join(top, "var/null"), unix.S_IFCHR|0o666, int(unix.Mkdev(1, 3))))
	var before unix.Stat_t
	must(unix.Lstat(filepath.Join(top, "var/right"), &before))
	res := Own([]string{top}, Options{UID: addonUID, GID: addonGID})
	owner := func(p string) [2]uint32 {
		var st unix.Stat_t
		must(unix.Lstat(filepath.Join(root, p), &st))
		return [2]uint32{st.Uid, st.Gid}
	}
	addon, rootOwner := [2]uint32{addonUID, addonGID}, [2]uint32{0, 0}
	for p, want := range map[string][2]uint32{
		"addon": addon, "addon/var": addon, "addon/var/x": addon, "addon/var/right": addon,
		"outside/shadow": rootOwner, "addon/var/shadow": rootOwner, "addon/var/null": rootOwner,
	} {
		if got := owner(p); got != want {
			t.Errorf("%s is %v, want %v", p, got, want)
		}
	}
	var after unix.Stat_t
	must(unix.Lstat(filepath.Join(top, "var/right"), &after))
	if after.Ctim != before.Ctim {
		t.Error("an entry that had its owner was touched")
	}
	if res.Devices != 1 || res.Symlinks != 1 || res.Fixed != 3 || res.Problem() != "" || res.Left() != 1 {
		t.Errorf("result %+v", res)
	}
}

// The quick check (task 110): a top directory and its direct entries, with the walk's rules - a wrong
// owner there is counted, a link there is not followed - and nothing below a directory of the top.
func TestOwnQuickDepth(t *testing.T) {
	root := tree(t, "addon/var/deep/db", "addon/var/pid", "addon/settings.js", "outside/secret")
	top := filepath.Join(root, "addon")
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(top, "link")); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	f.set(t, filepath.Join(top, "var/deep/db"), 0, 0)
	f.set(t, filepath.Join(top, "var/pid"), 0, 0)
	for _, p := range []string{top, filepath.Join(top, "var"), filepath.Join(top, "var/deep"), filepath.Join(top, "settings.js")} {
		f.set(t, p, addonUID, addonGID)
	}
	quick := func() Result {
		o := opts(f)
		o.DryRun, o.MaxDepth = true, QuickDepth
		return Own([]string{top}, o)
	}
	// top, var, settings.js and the link; var's entries are not looked at
	if res := quick(); res.Checked != 4 || res.Wrong != 0 || res.Symlinks != 1 || res.TooDeep != 1 || res.Problem() != "" {
		t.Errorf("clean top: %+v", res)
	}
	// a root-owned entry at the top is found
	f.set(t, filepath.Join(top, "settings.js"), 0, 0)
	if res := quick(); res.Wrong != 1 || res.RootOwned != 1 || res.FirstRootOwned != filepath.Join(top, "settings.js") {
		t.Errorf("root-owned at the top: %+v", res)
	}
	// the whole walk finds all three
	o := opts(f)
	o.DryRun = true
	if res := Own([]string{top}, o); res.RootOwned != 3 || res.Checked != 7 || len(f.chowned) != 0 {
		t.Errorf("the whole walk: %+v, %d chowns", res, len(f.chowned))
	}
	if f.times(t, filepath.Join(root, "outside/secret")) != 0 {
		t.Error("the link was followed")
	}
}

// openccu-lite B-252: TightenModes closes a confined addon's tree to other users - directories to
// at most 0751, non-www regular files to at most 0640 - and leaves the www subtree world-readable.
func TestOwnTightensModes(t *testing.T) {
	root := tree(t, "addon/var/db", "addon/var/sub/secret", "addon/etc/conf", "addon/www/index.html", "addon/www/css/app.css", "addon/already")
	top := filepath.Join(root, "addon")
	must := func(p string, m os.FileMode) {
		t.Helper()
		if err := os.Chmod(filepath.Join(root, p), m); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"addon", "addon/var", "addon/var/sub", "addon/etc", "addon/www", "addon/www/css"} {
		must(d, 0o755)
	}
	for _, f := range []string{"addon/var/db", "addon/var/sub/secret", "addon/etc/conf", "addon/www/index.html", "addon/www/css/app.css"} {
		must(f, 0o644)
	}
	must("addon/already", 0o600) // a file already tight is not touched
	f := newFake()
	o := opts(f)
	o.TightenModes = true
	o.PublicDirs = []string{filepath.Join(top, "www")}
	res := Own([]string{top}, o)
	if res.Problem() != "" {
		t.Fatalf("problem: %+v", res)
	}
	perm := func(p string) os.FileMode {
		st, err := os.Stat(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		return st.Mode().Perm()
	}
	// private directories become 0751 (traversable, not listable)
	for _, d := range []string{"addon/var", "addon/var/sub", "addon/etc"} {
		if perm(d) != 0o751 {
			t.Errorf("%s is %o, want 0751", d, perm(d))
		}
	}
	// the addon's own directory (it holds www, and the web server opens it) and the www tree stay
	// world-readable
	for _, d := range []string{"addon", "addon/www", "addon/www/css"} {
		if perm(d) != 0o755 {
			t.Errorf("%s is %o, want 0755 (public)", d, perm(d))
		}
	}
	// private files lose the world (and group-write) bits; the already-tight file is untouched
	for _, ff := range []string{"addon/var/db", "addon/var/sub/secret", "addon/etc/conf"} {
		if perm(ff) != 0o640 {
			t.Errorf("%s is %o, want 0640", ff, perm(ff))
		}
	}
	if perm("addon/already") != 0o600 {
		t.Errorf("an already-tight file was changed to %o", perm("addon/already"))
	}
	// the www files keep their mode: served to the browser
	for _, ff := range []string{"addon/www/index.html", "addon/www/css/app.css"} {
		if perm(ff) != 0o644 {
			t.Errorf("%s is %o, want the www file left 0644", ff, perm(ff))
		}
	}
	// counted: 3 private dirs (var, var/sub, etc) + 3 private files (addon, www, www/css and the two
	// www files and the already-tight file are not)
	if res.ModeTightened != 6 {
		t.Errorf("ModeTightened %d, want 6", res.ModeTightened)
	}
	// a second walk over the settled tree (the same fake, so its owners are the addon's now) changes
	// nothing
	f.reset()
	o2 := o
	o2.DryRun = true
	if res := Own([]string{top}, o2); res.Wrong != 0 || res.ModeTightened != 0 || res.Left() != 0 {
		t.Errorf("a settled tree: %+v", res)
	}
}

// The quick check flags a world-readable top so a tree installed 0755 is tightened at the next start.
func TestOwnTightenModesQuickCheck(t *testing.T) {
	root := tree(t, "addon/var/db", "addon/settings")
	top := filepath.Join(root, "addon")
	for _, p := range []string{"addon", "addon/var", "addon/var/db", "addon/settings"} {
		if err := os.Chmod(filepath.Join(root, p), map[bool]os.FileMode{true: 0o755, false: 0o644}[strings.HasSuffix(p, "var") || p == "addon"]); err != nil {
			t.Fatal(err)
		}
	}
	f := newFake()
	o := opts(f)
	o.TightenModes, o.DryRun, o.MaxDepth = true, true, QuickDepth
	res := Own([]string{top}, o)
	// the top and its direct entries (var, settings) are all mode-wrong; nothing below var is looked at
	if res.Wrong == 0 || res.ModeTightened != 0 {
		t.Errorf("quick check on a 0755 tree: %+v", res)
	}
}
