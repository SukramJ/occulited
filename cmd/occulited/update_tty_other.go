//go:build !linux

package main

import "os"

// isTerminal on a development system: a character device (see the Linux one).
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
