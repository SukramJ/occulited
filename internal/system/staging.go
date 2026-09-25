package system

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// StagingDir is where the unprivileged occulited writes big files (an addon archive, an
// update image) before the privilege boundary moves them into place with one rename: it is on
// the userfs like the destinations, and the helper's policy allows renames out of it.
var StagingDir = "/usr/local/etc/occulite/staging"

// stageFile stores src under StagingDir/<name> and returns the path and the size.
func stageFile(r Root, name string, src io.Reader) (string, int64, error) {
	dir := r.join(StagingDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", 0, err
	}
	n, err := io.Copy(f, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", 0, err
	}
	return path, n, nil
}

// StagedArchive is an addon archive already stored in the staging directory (B-4): the install
// API stores the upload while its request runs and installs it detached from the request, and
// Install, handed one, moves it into place instead of copying it again. Read reads the file, for
// an installer that does not know the type.
type StagedArchive struct {
	Path string
	Size int64
	f    *os.File
}

func (s *StagedArchive) Read(p []byte) (int, error) {
	if s.f == nil {
		f, err := os.Open(s.Path)
		if err != nil {
			return 0, err
		}
		s.f = f
	}
	return s.f.Read(p)
}

// Remove closes and deletes the staged file; after an install moved it into place there is
// nothing left to delete.
func (s *StagedArchive) Remove() {
	if s.f != nil {
		_ = s.f.Close()
		s.f = nil
	}
	_ = os.Remove(s.Path)
}

// StageAddonArchive stores an addon archive in the staging directory under name, refused as
// Install refuses it: larger than MaxAddonSize, or empty.
func StageAddonArchive(r Root, name string, src io.Reader) (*StagedArchive, error) {
	path, n, err := stageFile(r, name, io.LimitReader(src, MaxAddonSize+1))
	if err != nil {
		return nil, err
	}
	if n > MaxAddonSize {
		_ = os.Remove(path)
		return nil, fmt.Errorf("archive larger than %d MB", MaxAddonSize>>20)
	}
	if n < 64 {
		_ = os.Remove(path)
		return nil, errors.New("archive is empty")
	}
	return &StagedArchive{Path: path, Size: n}, nil
}
