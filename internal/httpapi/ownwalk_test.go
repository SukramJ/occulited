package httpapi

import (
	"fmt"
	"os"
	"testing"

	"github.com/hobbyquaker/occulited/internal/ownwalk"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/system"
)

// ownNoted is system.Priv in a test that confines an addon: the real operations, and the ownership
// walk (task 107) noted as the chown -R it replaced, one line per directory that exists. A test process
// cannot give files to an addon's uid; the walk is ownwalk's and priv's to test.
type ownNoted struct {
	priv.Local
	note func(string)
}

func (p ownNoted) OwnTree(_ string, dirs []string, uid int, _ priv.OwnTreeOptions) (ownwalk.Result, error) {
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			p.note(fmt.Sprintf("chown -R %d:%d %s", uid, uid, d))
		}
	}
	return ownwalk.Result{}, nil
}

func useOwnNoted(t *testing.T, note func(string)) {
	t.Helper()
	old := system.Priv
	system.Priv = ownNoted{note: note}
	t.Cleanup(func() { system.Priv = old })
}
