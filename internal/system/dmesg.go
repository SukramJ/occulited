package system

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Dmesg reads the kernel's ring buffer through `dmesg -r` (task 93): the kernel log of a box
// without a journal. The raw form keeps each message's priority (<6>) in front of the kernel's
// stamp; busybox's dmesg and util-linux's both print it. The ring buffer holds this boot only, and
// only as far back as its size allows.
type Dmesg struct {
	Root Root
	Run  Runner // nil = exec
	// Now is the wall clock the kernel's stamps are placed on (now - /proc/uptime); nil = time.Now
	Now func() time.Time
}

// dmesgRe is one raw line, "<6>[   12.345678] message"; the stamp is missing on a kernel built
// without CONFIG_PRINTK_TIME.
var dmesgRe = regexp.MustCompile(`^<(\d{1,4})>(?:\[\s*(\d+)\.(\d{1,9})\]\s?)?(.*)$`)

func (d Dmesg) read(ctx context.Context) ([]LogLine, error) {
	var out []byte
	var err error
	if d.Run != nil {
		out, err = d.Run(ctx, "dmesg", "-r")
	} else {
		out, err = exec.CommandContext(ctx, "dmesg", "-r").Output()
	}
	if err != nil {
		return nil, fmt.Errorf("dmesg: %w", err)
	}
	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	var start time.Time
	if up, ok := d.Root.Uptime(); ok {
		start = now.Add(-up)
	}
	return parseDmesg(out, start, d.Root.Hostname()), nil
}

// parseDmesg reads `dmesg -r`. A line without the priority prefix continues the message before
// it. start is when the kernel started on the wall clock; zero leaves the lines without a time.
func parseDmesg(out []byte, start time.Time, host string) []LogLine {
	lines := []LogLine{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		text := sc.Text()
		m := dmesgRe.FindStringSubmatch(text)
		if m == nil {
			if n := len(lines); n > 0 {
				lines[n-1].Message += "\n" + text
			} else if strings.TrimSpace(text) != "" {
				lines = append(lines, LogLine{Tag: "kernel", Host: host, Message: text})
			}
			continue
		}
		prio, _ := strconv.Atoi(m[1])
		l := LogLine{Tag: "kernel", Host: host, Severity: priorityName[prio&7], Facility: strconv.Itoa(prio >> 3), Message: m[4]}
		if m[2] != "" {
			sec, _ := strconv.ParseInt(m[2], 10, 64)
			us, _ := strconv.ParseInt((m[3] + "000000")[:6], 10, 64)
			l.MonotonicUS = sec*1_000_000 + us
			if !start.IsZero() {
				t := start.Add(time.Duration(l.MonotonicUS) * time.Microsecond)
				l.Time = t.Format("Jan _2 15:04:05")
				l.Timestamp = t.Format(time.RFC3339Nano)
			}
		}
		lines = append(lines, l)
	}
	return lines
}

// Read returns the newest matching lines, oldest first, at most q.Limit (default 500). The tag,
// the severity floor and the text filter as on busybox syslog; the unit and the time range are
// ignored. The ring buffer holds this boot only, so a query for another boot gets no lines.
func (d Dmesg) Read(q LogQuery) ([]LogLine, error) {
	if q.Limit <= 0 {
		q.Limit = 500
	}
	if !d.Root.IsThisBoot(q.Boot) {
		return []LogLine{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	all, err := d.read(ctx)
	if err != nil {
		return nil, err
	}
	match := lineMatcher(q)
	lines := []LogLine{}
	for _, l := range all {
		if match(l) {
			lines = append(lines, l)
		}
	}
	if len(lines) > q.Limit {
		lines = lines[len(lines)-q.Limit:]
	}
	return lines, nil
}

// Export hands over every matching line of the ring buffer, oldest first, with Read's filters.
func (d Dmesg) Export(ctx context.Context, q LogQuery, emit func(LogLine) error) error {
	if !d.Root.IsThisBoot(q.Boot) {
		return nil
	}
	all, err := d.read(ctx)
	if err != nil {
		return err
	}
	match := lineMatcher(q)
	for _, l := range all {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !match(l) {
			continue
		}
		if err := emit(l); err != nil {
			return err
		}
	}
	return nil
}
