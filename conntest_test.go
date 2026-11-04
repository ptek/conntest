package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ms(n int64) Result { return Result{Latency: time.Duration(n) * time.Millisecond} }

func TestCellBuckets(t *testing.T) {
	cases := []struct {
		ms    int64
		color string
		bar   rune
	}{
		{0, ansiGreenBlack, '▁'}, {9, ansiGreenBlack, '▁'}, {10, ansiGreenBlack, '▂'},
		{70, ansiGreenBlack, '█'}, {80, ansiGreenBlack, '█'},
		{81, ansiOrangeGrn, '▁'}, {89, ansiOrangeGrn, '▁'}, {90, ansiOrangeGrn, '▂'}, {150, ansiOrangeGrn, '█'}, {160, ansiOrangeGrn, '█'},
		{161, ansiRedOrange, '▁'}, {230, ansiRedOrange, '█'}, {5000, ansiRedOrange, '█'},
	}
	for _, c := range cases {
		got := cell(ms(c.ms))
		if !strings.HasPrefix(got, c.color) || !strings.Contains(got, string(c.bar)) {
			t.Errorf("%dms: got %q, want color %q bar %q", c.ms, got, c.color, string(c.bar))
		}
	}
	if got := cell(Result{Err: errors.New("x")}); got != ansiRedBlack+"!"+ansiReset {
		t.Errorf("error cell = %q", got)
	}
}

func TestDisplayWraps(t *testing.T) {
	d := newDisplay(&strings.Builder{}, []string{"a", "b"})
	d.termWidth = func() int { return 23 } // (23 - 3) / 2 = 10 per column
	colWidth := d.colWidth()
	if colWidth != 10 {
		t.Fatalf("colWidth = %d", colWidth)
	}
	for i := 0; i < colWidth+1; i++ {
		d.cells["a"] = append(d.cells["a"], "x")
	}
	d.cells["b"] = []string{"y"}
	lines := d.lines()
	want := []string{
		"a          │ b",
		strings.Repeat("x", 10) + " │ y",
		"x          │",
	}
	if len(lines) != len(want) {
		t.Fatalf("want %d lines, got %d:\n%s", len(want), len(lines), strings.Join(lines, "\n"))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestCTLogAppendsJSONLines(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)

	ts := time.Date(2026, 11, 4, 15, 44, 0, 0, time.FixedZone("CET", 3600))
	for i := 0; i < 2; i++ {
		l, err := openCTLog("h")
		if err != nil {
			t.Fatal(err)
		}
		r := Result{Host: "h", Time: ts, Seq: i, Latency: 35 * time.Millisecond}
		if i == 1 {
			r.Err = errTimeout
		}
		if err := l.Log(r); err != nil {
			t.Fatal(err)
		}
		l.Close()
	}
	b, _ := os.ReadFile(filepath.Join(dir, "h.ctlog"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := []string{
		`{"time":"2026-11-04T15:44:00+01:00","target":"h","icmp_seq":0,"latency_ms":35}`,
		`{"time":"2026-11-04T15:44:00+01:00","target":"h","icmp_seq":1,"error":"timeout after 4 seconds","cause":"no reply: packet loss, host down, or ICMP filtered by a firewall"}`,
	}
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestStripIPv4Header(t *testing.T) {
	// Captured on macOS loopback: IPv4 header + ICMP echo reply.
	raw := []byte{0x45, 0, 0x0a, 0, 0x9e, 0xbc, 0, 0, 0x40, 0x01, 0, 0, 0x7f, 0, 0, 1, 0x7f, 0, 0, 1,
		0, 0, 0x92, 0xc3, 0x04, 0xd2, 0, 1, 0x68, 0x69}
	if got := stripIPv4Header(raw); len(got) != 10 || got[0] != 0 {
		t.Errorf("header not stripped: %x", got)
	}
	plain := raw[20:]
	if got := stripIPv4Header(plain); &got[0] != &plain[0] {
		t.Errorf("plain ICMP message was modified")
	}
}

func TestExecPingerLoopback(t *testing.T) {
	p, err := newExecPinger("127.0.0.1")
	if err != nil {
		t.Skip(err)
	}
	lat, err := p.Ping(context.Background(), 0)
	if err != nil || lat < 0 {
		t.Errorf("lat=%v err=%v", lat, err)
	}
}
