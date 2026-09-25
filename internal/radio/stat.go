package radio

import (
	"os"
	"syscall"
)

// statIDs is a file's owner and group from its FileInfo.
func statIDs(info os.FileInfo) ([2]int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return [2]int{}, false
	}
	return [2]int{int(st.Uid), int(st.Gid)}, true
}
