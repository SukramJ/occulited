package meta

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LoadResult says where a document came from.
type LoadResult struct {
	Doc *Document
	// RecoveredFromBackup is true when meta.json was unusable and meta.json.bak was loaded instead.
	// Callers must surface this loudly (docs/meta-format.md).
	RecoveredFromBackup bool
	// Fresh is true when neither file existed and a new document was created.
	Fresh bool
}

// Load reads path, falling back to path+".bak" when the primary is missing or unparsable; when
// neither exists it returns a fresh document.
func Load(path string) (*LoadResult, error) {
	doc, err := readDoc(path)
	if err == nil {
		return &LoadResult{Doc: doc}, nil
	}
	primaryErr := err
	if errors.Is(err, os.ErrNotExist) {
		if _, berr := os.Stat(path + ".bak"); errors.Is(berr, os.ErrNotExist) {
			return &LoadResult{Doc: NewDocument(), Fresh: true}, nil
		}
	}
	doc, err = readDoc(path + ".bak")
	if err != nil {
		return nil, fmt.Errorf("%s unusable (%v) and backup unusable (%v)", path, primaryErr, err)
	}
	return &LoadResult{Doc: doc, RecoveredFromBackup: true}, nil
}

func readDoc(path string) (*Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc Document
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := doc.Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	return &doc, nil
}

// Save writes doc atomically: temp file in the same directory, fsync, rename; the previous file
// is kept as path+".bak".
func Save(path string, doc *Document) error {
	b, err := Marshal(doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".meta-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, path+".bak"); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// Saver returns a saver for New that persists to path.
func Saver(path string) func(*Document) error {
	return func(d *Document) error { return Save(path, d) }
}

// Marshal encodes a document with sorted keys and stable formatting for diffs.
func Marshal(doc *Document) ([]byte, error) {
	// encoding/json sorts map keys already; nodes keep their order because they are slices.
	return json.MarshalIndent(doc, "", "  ")
}

// sortedKeys is a small helper for deterministic iteration in tests and output.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
