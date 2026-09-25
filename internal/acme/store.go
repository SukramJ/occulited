package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The files under <state>/acme/ (docs/config.md). Everything is 0600 to the occulite user; the
// directory is on the userfs, so it is in every backup.
const (
	fileSettings = "settings.json" // Settings, secrets included
	fileAccount  = "account.key"   // the ACME account key, PEM (one key, every directory)
	fileRegs     = "account.json"  // registrations by directory URL
	fileCert     = "cert.pem"      // the issued chain, leaf first
	fileKey      = "key.pem"       // its private key
	fileLast     = "last.json"     // the last attempt
	fileDomain   = "domain.json"   // the DNS domain FollowDomain saw last
)

// store is the directory.
type store struct{ dir string }

func (s store) path(name string) string { return filepath.Join(s.dir, name) }

func (s store) ensure() error { return os.MkdirAll(s.dir, 0o700) }

// write is atomic and 0600.
func (s store) write(name string, b []byte) error {
	if err := s.ensure(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "."+name+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
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
	return os.Rename(tmp.Name(), s.path(name))
}

func (s store) read(name string) ([]byte, error) { return os.ReadFile(s.path(name)) }

func (s store) writeJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.write(name, append(b, '\n'))
}

// readJSON: a missing file leaves v alone and reports false.
func (s store) readJSON(name string, v any) (bool, error) {
	b, err := s.read(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

// accountKey loads the account key, creating one (P-256) the first time.
func (s store) accountKey() (*ecdsa.PrivateKey, error) {
	b, err := s.read(fileAccount)
	if err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, fmt.Errorf("%s: not PEM", fileAccount)
		}
		k, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fileAccount, err)
		}
		return k, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	if err := s.write(fileAccount, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})); err != nil {
		return nil, err
	}
	return k, nil
}

// registrations is account.json: what each directory answered when the key registered there.
type registrations map[string]json.RawMessage

func (s store) registrations() registrations {
	regs := registrations{}
	_, _ = s.readJSON(fileRegs, &regs)
	return regs
}
