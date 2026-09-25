// Package unitshow reads the output of `systemctl show`: one block of Key=value lines per unit
// (or one for the manager), the blocks separated by a blank line. The boot timing of task 94 and
// the boot timeline of task 93 both read it.
package unitshow

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// Blocks are the blocks in the order systemctl printed them; empty blocks are left out.
func Blocks(out []byte) []map[string]string {
	var all []map[string]string
	block := map[string]string{}
	flush := func() {
		if len(block) > 0 {
			all = append(all, block)
		}
		block = map[string]string{}
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	// After= of a target lists a hundred units: far below this, and far above bufio's 64 KiB
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			block[k] = strings.TrimSpace(v)
		}
	}
	flush()
	return all
}

// ByID are the unit blocks keyed by their Id property; a block without one is left out.
func ByID(out []byte) map[string]map[string]string {
	all := map[string]map[string]string{}
	for _, b := range Blocks(out) {
		if id := b["Id"]; id != "" {
			all[id] = b
		}
	}
	return all
}

// Micros is a *TimestampMonotonic property: microseconds since the kernel started; 0 when it is
// not set, not a number or not positive.
func Micros(props map[string]string, key string) int64 {
	n, err := strconv.ParseInt(props[key], 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// Words splits a list property such as After= into its entries. systemctl quotes an entry that holds
// a backslash and escapes the backslash inside it - `"systemd-fsck@dev-disk-by\\x2dlabel-userfs.service"`
// is the unit systemd-fsck@dev-disk-by\x2dlabel-userfs.service. Split on the spaces alone, the
// quoted name matches no unit, and the critical chain took another branch there (task 93, measured
// against systemd-analyze on the Charly).
func Words(v string) []string {
	var words []string
	var b strings.Builder
	in := false      // inside a word
	quote := byte(0) // the quote character while inside quotes
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case quote != 0:
			switch {
			case c == quote:
				quote = 0
			case c == '\\' && i+1 < len(v) && (v[i+1] == '\\' || v[i+1] == quote):
				i++
				b.WriteByte(v[i])
			default:
				b.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote, in = c, true
		case c == ' ' || c == '\t' || c == '\n':
			if in {
				words = append(words, b.String())
				b.Reset()
				in = false
			}
		default:
			b.WriteByte(c)
			in = true
		}
	}
	if in {
		words = append(words, b.String())
	}
	return words
}
