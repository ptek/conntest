package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// logEntry is one line in a .ctlog file. Field order matches the spec.
type logEntry struct {
	Time      string `json:"time"`
	Target    string `json:"target"`
	SourceIP  string `json:"source_ip,omitempty"`
	ICMPSeq   int    `json:"icmp_seq"`
	LatencyMs *int64 `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
	Cause     string `json:"cause,omitempty"`
}

// ctLogger appends JSON-lines results to <host>.ctlog.
type ctLogger struct {
	mu sync.Mutex
	f  *os.File
}

func openCTLog(host string) (*ctLogger, error) {
	f, err := os.OpenFile(host+".ctlog", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &ctLogger{f: f}, nil
}

func (l *ctLogger) Log(r Result) error {
	e := logEntry{
		Time:     r.Time.Format(time.RFC3339),
		Target:   r.Host,
		SourceIP: r.SourceIP,
		ICMPSeq:  r.Seq,
	}
	if r.Err != nil {
		e.Error = r.Err.Error()
		e.Cause = probableCause(r.Err)
	} else {
		ms := r.Latency.Milliseconds()
		e.LatencyMs = &ms
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = fmt.Fprintf(l.f, "%s\n", b)
	return err
}

func (l *ctLogger) Close() error { return l.f.Close() }
