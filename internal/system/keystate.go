package system

import (
	"context"
	"os"
)

// SecurityKeyState is SecurityKeySet for the Status page's warning (task 81), which has to tell
// a failed read from a set key: known is false without crypttool (a development root), err is a
// crypttool that could not be run through the helper. crypttool -v -t 0 exits non-zero when a
// user key replaced the default.
func (r Root) SecurityKeyState(ctx context.Context) (set, known bool, err error) {
	tool := r.join("/bin/crypttool")
	if _, statErr := os.Stat(tool); statErr != nil || string(r) != "/" {
		return false, false, nil
	}
	res, err := Priv.Run(ctx, tool, []string{"-v", "-t", "0"}, nil)
	if err != nil {
		return false, true, err
	}
	return res.Exit != 0, true, nil
}
