package system

import "syscall"

func diskUsage(path string) (Disk, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Disk{}, false
	}
	total := int64(st.Blocks) * int64(st.Bsize) / 1024
	free := int64(st.Bavail) * int64(st.Bsize) / 1024
	return Disk{TotalKB: total, UsedKB: total - free}, true
}

// freeBytes is the space an unprivileged writer has left on the filesystem of path (statfs's
// f_bavail), as df shows it.
func freeBytes(path string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}

// sameFilesystem reports whether a and b are on the same mounted filesystem. Both are resolved
// first: /media/usb0 is a symlink into the userfs on a box with no stick, and the point of the
// question is exactly what that symlink hides.
func sameFilesystem(a, b string) (bool, bool) {
	var sa, sb syscall.Stat_t
	if err := syscall.Stat(a, &sa); err != nil {
		return false, false
	}
	if err := syscall.Stat(b, &sb); err != nil {
		return false, false
	}
	return sa.Dev == sb.Dev, true
}
