package radio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// openccu-lite B-298: hmipserver keeps its metadata (clients' value usage, channel modes, smoke
// detector groups, suppressed service messages) and its direct links in two JSON files that it
// rewrites in place, truncating first. A stop that lands between the truncation and the write -
// a shutdown while a client is still setting metadata - leaves the file empty, and the server
// never recovers from an empty or unparsable file: it logs a stack trace on every status event and
// refuses every write until the file is fixed by hand.
//
// So the prep and the stop step keep a last good copy of each file (<file>.good, written while
// nothing runs: before the server starts and after it is gone). A file that is empty or not a JSON
// object when one of them looks is replaced by that copy - what changed since the copy was taken
// is lost, the rest is back - and without a copy it is moved aside, so the server starts with an
// empty store and writes a fresh one. The broken file is kept beside it for diagnosis
// (<file>.broken-<UTC>, the newest three).
//
// The store is the server's directory, so nothing here follows a link: files are opened with
// O_NOFOLLOW, replaced by rename, and whatever is not a regular file is left alone.

// hmipStores are the files hmipserver rewrites in place.
var hmipStores = []string{"/etc/config/crRFD/data/metaData.conf", "/etc/config/crRFD/data/linkData.conf"}

const (
	storeGoodSuffix  = ".good"
	storeBrokenInfix = ".broken-"
	storeKeepBroken  = 3
	storeMaxSize     = 64 << 20
)

// storeNow is the clock of the broken files' names; a variable for the tests.
var storeNow = time.Now

// guardHMIPStores runs the check for each store; step is "prep" or "stopped", for the log.
func guardHMIPStores(d Detector, step string, logf func(string, ...any)) {
	for _, s := range hmipStores {
		guardStore(d.path(s), step, logf)
	}
}

// guardStore keeps path's last good copy current, or repairs path from it.
func guardStore(path, step string, logf func(string, ...any)) {
	name := filepath.Base(path)
	content, err := readStore(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return // the server starts with an empty store and writes the file at its first change
	case err != nil:
		logf("%s hmipserver: %s left alone: %v", step, name, err)
		return
	case validStore(content):
		good := path + storeGoodSuffix
		if cur, err := readStore(good); err == nil && bytes.Equal(cur, content) {
			return
		}
		if err := writeStore(good, content); err != nil {
			logf("%s hmipserver: the last good copy of %s could not be written: %v", step, name, err)
		}
		return
	}
	// broken: kept aside, then the last good copy or nothing
	what := "empty"
	if len(bytes.TrimSpace(content)) > 0 {
		what = "not valid JSON"
	}
	aside := path + storeBrokenInfix + storeNow().UTC().Format("20060102T150405Z")
	if err := os.Rename(path, aside); err != nil {
		logf("%s hmipserver: %s is %s (%d bytes) and could not be moved aside: %v", step, name, what, len(content), err)
		return
	}
	Own(aside, "hmipserver", "hmipserver", 0o600)
	pruneBroken(path)
	good, err := readStore(path + storeGoodSuffix)
	if err != nil || !validStore(good) {
		logf("%s hmipserver: %s was %s (%d bytes) and there is no good copy: moved aside to %s, the server starts with an empty store", step, name, what, len(content), filepath.Base(aside))
		return
	}
	if err := writeStore(path, good); err != nil {
		logf("%s hmipserver: %s was %s (%d bytes), moved aside to %s; the last good copy could not be restored: %v", step, name, what, len(content), filepath.Base(aside), err)
		return
	}
	logf("%s hmipserver: %s was %s (%d bytes): restored from its last good copy (%d bytes), the broken file kept as %s", step, name, what, len(content), len(good), filepath.Base(aside))
}

// validStore: a non-empty JSON object, which is what the server writes and reads.
func validStore(b []byte) bool {
	var m map[string]json.RawMessage
	return len(bytes.TrimSpace(b)) > 0 && json.Unmarshal(b, &m) == nil && m != nil
}

// readStore reads a regular file without following a link.
func readStore(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("a link, not a file")
		}
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if st.Size() > storeMaxSize {
		return nil, fmt.Errorf("%d bytes, larger than a store can be", st.Size())
	}
	return io.ReadAll(io.LimitReader(f, storeMaxSize+1))
}

// writeStore replaces path atomically with content: the server's, 0600, synced before the rename.
func writeStore(path string, content []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	Own(tmp, "hmipserver", "hmipserver", 0o600)
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	if df, err := os.Open(dir); err == nil {
		_ = df.Sync()
		df.Close()
	}
	return nil
}

// pruneBroken keeps the newest storeKeepBroken broken copies of path (their names sort by time).
func pruneBroken(path string) {
	dir, base := filepath.Dir(path), filepath.Base(path)+storeBrokenInfix
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var broken []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), base) && e.Type().IsRegular() {
			broken = append(broken, e.Name())
		}
	}
	sort.Strings(broken)
	for len(broken) > storeKeepBroken {
		_ = os.Remove(filepath.Join(dir, broken[0]))
		broken = broken[1:]
	}
}
