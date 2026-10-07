package main

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal says whether f is a terminal: a TCGETS that succeeds. A character device alone is
// not one - /dev/null is a character device, and `occulited update install </dev/null` from a
// script must not count as a terminal that answered.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
