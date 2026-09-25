package system

import (
	"io/fs"
	"syscall"
)

// allocatedBytes is the space a file takes on its filesystem: st_blocks × 512, what `du` and
// `journalctl --disk-usage` count and what journald's SystemMaxUse/RuntimeMaxUse limit. A journal
// file is preallocated in chunks, so its apparent size (the length) is far larger than what it
// uses - on the lab OVA 58.7 MB apparent against 29.2 MB allocated. Where the platform gives no
// block count, the length is all there is.
func allocatedBytes(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return info.Size()
}
