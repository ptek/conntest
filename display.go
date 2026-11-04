package main

import (
	"fmt"
	"io"
	"strings"
)

const colWidth = 16 // bars per column, matching the spec's example layout

var bars = []rune("▁▂▃▄▅▆▇█")

const (
	ansiReset      = "\x1b[0m"
	ansiGreenBlack = "\x1b[32;40m"
	ansiOrangeGrn  = "\x1b[38;5;208;42m"
	ansiRedOrange  = "\x1b[31;48;5;208m"
	ansiRedBlack   = "\x1b[31;40m"
)

// cell returns the coloured glyph for a single result.
func cell(r Result) string {
	if r.Err != nil {
		return ansiRedBlack + "!" + ansiReset
	}
	ms := r.Latency.Milliseconds()
	var color string
	var idx int64
	switch {
	case ms <= 80:
		color, idx = ansiGreenBlack, ms/10
	case ms <= 160:
		color, idx = ansiOrangeGrn, (ms-80)/10
	default:
		color, idx = ansiRedOrange, (ms-160)/10
	}
	if idx < 0 {
		idx = 0
	}
	if idx > int64(len(bars)-1) {
		idx = int64(len(bars) - 1)
	}
	return color + string(bars[idx]) + ansiReset
}

// display renders one column per host; bars wrap to a new row once a column fills up.
type display struct {
	w       io.Writer
	hosts   []string
	cells   map[string][]string
	printed int // lines written by the previous render
}

func newDisplay(w io.Writer, hosts []string) *display {
	d := &display{w: w, hosts: hosts, cells: make(map[string][]string, len(hosts))}
	for _, h := range hosts {
		d.cells[h] = nil
	}
	return d
}

func (d *display) Start() { fmt.Fprint(d.w, "\x1b[?25l"); d.render() }
func (d *display) Stop()  { fmt.Fprint(d.w, "\x1b[?25h") }

func (d *display) Add(r Result) {
	d.cells[r.Host] = append(d.cells[r.Host], cell(r))
	d.render()
}

func (d *display) render() {
	var sb strings.Builder
	if d.printed > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", d.printed)
	}
	lines := d.lines()
	for _, l := range lines {
		sb.WriteString("\x1b[2K" + l + "\n")
	}
	d.printed = len(lines)
	fmt.Fprint(d.w, sb.String())
}

func (d *display) lines() []string {
	pad := func(s string, n int) string {
		if n > 0 {
			return s + strings.Repeat(" ", n)
		}
		return s
	}
	hdr, sep := "|", "|"
	for _, h := range d.hosts {
		name := h
		if len(name) > colWidth {
			name = name[:colWidth]
		}
		hdr += " " + pad(name, colWidth-len(name)) + " |"
		sep += strings.Repeat("-", colWidth+2) + "|"
	}
	rows := 1
	for _, h := range d.hosts {
		if n := (len(d.cells[h]) + colWidth - 1) / colWidth; n > rows {
			rows = n
		}
	}
	out := []string{hdr, sep}
	for row := 0; row < rows; row++ {
		line := "|"
		for _, h := range d.hosts {
			cs := d.cells[h]
			lo, hi := row*colWidth, (row+1)*colWidth
			if lo > len(cs) {
				lo = len(cs)
			}
			if hi > len(cs) {
				hi = len(cs)
			}
			seg := cs[lo:hi]
			line += " " + pad(strings.Join(seg, ""), colWidth-len(seg)) + " |"
		}
		out = append(out, line)
	}
	return out
}
