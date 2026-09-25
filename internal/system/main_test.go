package system

import (
	"os"
	"testing"

	"github.com/hobbyquaker/occulited/internal/ownwalk"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// testLocal is the package's Priv in its tests: the real operations, except that the ownership walk
// (task 107) changes nothing. A test process cannot give files to an addon's uid, and what the walk
// does is ownwalk's and priv's to test; before the walk, the tests' command recorder took the
// `chown -R` the same way. A test that looks at the walk sets a Priv of its own.
type testLocal struct{ priv.Local }

func (testLocal) OwnTree(string, []string, int, priv.OwnTreeOptions) (ownwalk.Result, error) {
	return ownwalk.Result{}, nil
}

func TestMain(m *testing.M) {
	Priv = testLocal{}
	os.Exit(m.Run())
}
