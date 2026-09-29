package system

// openccu-lite task 296: a backup made with a non-default BidCos security key (the "System-
// Sicherheitsschlüssel" of the CCU that made it) brings rfd's key store along as it is - the
// devices paired with it keep working without the passphrase. The passphrase is still what a
// later key change, a re-key of the devices, a pairing with another central or a restore onto a
// system with another key needs, so the restore and the device import ask for it as a check: the
// user learns whether the passphrase they have is the right one while the old system is still
// within reach. The check never blocks.
//
// The check needs no key material and no external tool: createBackup.sh signs the inner archive
// with `crypttool -s -t 1`, and crypttool's signature is the MD5 of the data encrypted with the
// key (AES-128, one block), the key being the MD5 of the passphrase. So a typed passphrase is
// checked by computing that one block and comparing it with the backup's signature file; the
// archive's MD5 is computed once per upload. The passphrase is used for that comparison only:
// never stored, never logged, never in an answer.

import (
	"archive/tar"
	"context"
	"crypto/aes"
	"crypto/md5" // crypttool's own construction, compared, not chosen
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
)

// BackupSignature is what a .sbk says about the key it was signed with.
type BackupSignature struct {
	// KeyIndex is the outer key_index file: 0 is the factory key, every key change counts one up.
	KeyIndex int
	// Signature is the outer signature file, lower-case hex ("" when the backup has none).
	Signature string
	digest    [16]byte
	hasDigest bool
}

// ReadBackupSignature reads the key index and the signature of a .sbk and the MD5 of its inner
// usr_local.tar.gz (streamed, nothing is extracted).
func ReadBackupSignature(sbk string) (BackupSignature, error) {
	var s BackupSignature
	f, err := os.Open(sbk)
	if err != nil {
		return s, err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return s, fmt.Errorf("the backup archive: %w", err)
		}
		switch path.Base(h.Name) {
		case "key_index":
			b, _ := io.ReadAll(io.LimitReader(tr, 64))
			s.KeyIndex, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		case "signature":
			b, _ := io.ReadAll(io.LimitReader(tr, 256))
			s.Signature = strings.ToLower(strings.TrimSpace(string(b)))
		case "usr_local.tar.gz":
			m := md5.New()
			if _, err := io.Copy(m, tr); err != nil {
				return s, fmt.Errorf("usr_local.tar.gz: %w", err)
			}
			copy(s.digest[:], m.Sum(nil))
			s.hasDigest = true
		}
	}
	if !s.hasDigest {
		return s, ErrNotSBK
	}
	return s, nil
}

// NonDefault says whether the backup was made with a key other than the factory key.
func (s BackupSignature) NonDefault() bool { return s.KeyIndex > 0 }

// Matches says whether passphrase is the one the backup was signed with. Surrounding blanks are
// dropped, as the firmware's restore script reads the key with `read`.
func (s BackupSignature) Matches(passphrase string) bool {
	passphrase = strings.TrimSpace(passphrase)
	if passphrase == "" || !s.hasDigest || len(s.Signature) != 32 {
		return false
	}
	want, err := hex.DecodeString(s.Signature)
	if err != nil {
		return false
	}
	key := md5.Sum([]byte(passphrase)) // crypttool's key derivation
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return false
	}
	var got [16]byte
	block.Encrypt(got[:], s.digest[:])
	return subtle.ConstantTimeCompare(got[:], want) == 1
}

// The verdicts of the passphrase check, as the API and the import record say them.
const (
	// KeyCheckNone: the backup uses the factory key; there is no passphrase to know.
	KeyCheckNone = "none"
	// KeyCheckMatch: the passphrase given is the backup's.
	KeyCheckMatch = "match"
	// KeyCheckMismatch: a passphrase was given and it is not the backup's.
	KeyCheckMismatch = "mismatch"
	// KeyCheckSkipped: the backup has its own key and no passphrase was given.
	KeyCheckSkipped = "skipped"
)

// KeyCheck is the verdict on passphrase for this backup.
func (s BackupSignature) KeyCheck(passphrase string) string {
	switch {
	case s.Matches(passphrase):
		return KeyCheckMatch
	case !s.NonDefault():
		return KeyCheckNone
	case strings.TrimSpace(passphrase) == "":
		return KeyCheckSkipped
	}
	return KeyCheckMismatch
}

// SystemKeyMatches checks passphrase against this system's own BidCos security key: set says
// whether a key other than the factory key is set, match whether passphrase is it, known false
// without crypttool (a development root). A passphrase crypttool could never have been given (the
// firmware takes letters, digits and _ only) is a mismatch without asking the helper - the helper
// would refuse it, and its refusal names the arguments.
func (r Root) SystemKeyMatches(ctx context.Context, passphrase string) (set, match, known bool) {
	set, known, err := r.SecurityKeyState(ctx)
	if !known || err != nil {
		return false, false, false
	}
	passphrase = strings.TrimSpace(passphrase)
	if !set || !securityKeyRe.MatchString(passphrase) || len(passphrase) > 128 {
		return set, false, true
	}
	res, err := Priv.Run(ctx, r.join("/bin/crypttool"), []string{"-v", "-t", "3", "-k", passphrase}, nil)
	return true, err == nil && res.Exit == 0, true
}
