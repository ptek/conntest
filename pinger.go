package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	pingInterval = 5 * time.Second
	pingTimeout  = 4 * time.Second
)

// Result is the outcome of a single echo request.
type Result struct {
	Host     string
	Time     time.Time
	Seq      int
	SourceIP string
	Latency  time.Duration
	Err      error
}

// pingError carries a probable cause alongside the exact error text.
type pingError struct {
	msg   string
	cause string
}

func (e *pingError) Error() string { return e.msg }

func probableCause(err error) string {
	var pe *pingError
	if errors.As(err, &pe) {
		return pe.cause
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "no such host"):
		return "DNS resolution failed"
	case strings.Contains(s, "network is unreachable"):
		return "no route to host: interface down or missing default gateway"
	case strings.Contains(s, "no route to host"):
		return "no route: host not on local network and no gateway responds"
	case strings.Contains(s, "operation not permitted"), strings.Contains(s, "permission denied"):
		return "insufficient privileges for ICMP sockets"
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline"):
		return "no reply: packet loss, host down, or ICMP filtered by a firewall"
	}
	return "unknown"
}

var errTimeout = &pingError{
	msg:   fmt.Sprintf("timeout after %d seconds", int(pingTimeout.Seconds())),
	cause: "no reply: packet loss, host down, or ICMP filtered by a firewall",
}

// pinger sends one echo request and waits for the matching reply.
type pinger interface {
	Ping(ctx context.Context, seq int) (sourceIP string, latency time.Duration, err error)
	Close() error
}

// newPinger prefers raw ICMP, then unprivileged datagram ICMP, then /sbin/ping.
func newPinger(host string) (pinger, error) {
	addr, err := net.ResolveIPAddr("ip", host)
	if err != nil {
		return nil, err
	}
	if p, err := newSocketPinger(addr); err == nil {
		return p, nil
	}
	return newExecPinger(host)
}

// socketPinger uses golang.org/x/net/icmp.
type socketPinger struct {
	conn  *icmp.PacketConn
	dst   net.Addr
	v6    bool
	rawID bool // ID is preserved only on raw sockets; udp sockets rewrite it
	id    int
	proto int
	buf   []byte
}

func newSocketPinger(addr *net.IPAddr) (*socketPinger, error) {
	v6 := addr.IP.To4() == nil
	rawNet, udpNet, proto := "ip4:icmp", "udp4", ipv4.ICMPTypeEcho.Protocol()
	if v6 {
		rawNet, udpNet, proto = "ip6:ipv6-icmp", "udp6", ipv6.ICMPTypeEchoRequest.Protocol()
	}
	p := &socketPinger{v6: v6, proto: proto, id: rand.Intn(0xffff), buf: make([]byte, 1500)}
	if c, err := icmp.ListenPacket(rawNet, ""); err == nil {
		p.conn, p.dst, p.rawID = c, addr, true
		return p, nil
	}
	c, err := icmp.ListenPacket(udpNet, "")
	if err != nil {
		return nil, err
	}
	p.conn, p.dst = c, &net.UDPAddr{IP: addr.IP, Zone: addr.Zone}
	return p, nil
}

func (p *socketPinger) Close() error { return p.conn.Close() }

func (p *socketPinger) Ping(ctx context.Context, seq int) (string, time.Duration, error) {
	var typ icmp.Type = ipv4.ICMPTypeEcho
	if p.v6 {
		typ = ipv6.ICMPTypeEchoRequest
	}
	payload := []byte(fmt.Sprintf("conntest %d %d", p.id, seq))
	msg := icmp.Message{Type: typ, Code: 0, Body: &icmp.Echo{ID: p.id, Seq: seq & 0xffff, Data: payload}}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return "", 0, err
	}

	start := time.Now()
	deadline := start.Add(pingTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := p.conn.SetDeadline(deadline); err != nil {
		return "", 0, err
	}
	if _, err := p.conn.WriteTo(wb, p.dst); err != nil {
		return "", 0, err
	}

	for {
		n, peer, err := p.conn.ReadFrom(p.buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return "", 0, errTimeout
			}
			return "", 0, err
		}
		rm, err := icmp.ParseMessage(p.proto, stripIPv4Header(p.buf[:n]))
		if err != nil {
			continue
		}
		src := peerIP(peer)
		switch body := rm.Body.(type) {
		case *icmp.Echo:
			if rm.Type != ipv4.ICMPTypeEchoReply && rm.Type != ipv6.ICMPTypeEchoReply {
				continue
			}
			if p.rawID && body.ID != p.id {
				continue
			}
			if body.Seq != seq&0xffff || !strings.HasPrefix(string(body.Data), string(payload)) {
				continue
			}
			return src, time.Since(start), nil
		case *icmp.DstUnreach:
			if !p.ownsInner(body.Data, seq) {
				continue
			}
			return src, 0, unreachError(rm.Type, rm.Code, src)
		case *icmp.TimeExceeded:
			if !p.ownsInner(body.Data, seq) {
				continue
			}
			return src, 0, &pingError{
				msg:   fmt.Sprintf("time exceeded reported by %s", src),
				cause: "TTL expired in transit: routing loop or path too long",
			}
		}
	}
}

// stripIPv4Header removes a leading IPv4 header if present. macOS datagram ICMP
// sockets sometimes deliver loopback replies with the IP header still attached.
func stripIPv4Header(b []byte) []byte {
	if len(b) < ipv4.HeaderLen || b[0]>>4 != 4 || b[9] != 1 {
		return b
	}
	hl := int(b[0]&0x0f) * 4
	if hl < ipv4.HeaderLen || hl > len(b) {
		return b
	}
	return b[hl:]
}

// ownsInner checks whether an ICMP error's embedded datagram is our echo for seq.
func (p *socketPinger) ownsInner(data []byte, seq int) bool {
	// Error payload = original IP header + first 8 bytes of our ICMP echo.
	var hdrLen int
	if p.v6 {
		hdrLen = ipv6.HeaderLen
	} else if len(data) > 0 {
		hdrLen = int(data[0]&0x0f) * 4
	}
	if len(data) < hdrLen+8 {
		return false
	}
	inner, err := icmp.ParseMessage(p.proto, data[hdrLen:])
	if err != nil {
		return false
	}
	e, ok := inner.Body.(*icmp.Echo)
	if !ok || e.Seq != seq&0xffff {
		return false
	}
	return !p.rawID || e.ID == p.id
}

func unreachError(t icmp.Type, code int, from string) error {
	cause := "destination unreachable"
	if t == ipv4.ICMPTypeDestinationUnreachable {
		switch code {
		case 0:
			cause = "network unreachable: no route on an intermediate router"
		case 1:
			cause = "host unreachable: target offline or ARP failure on last hop"
		case 2:
			cause = "protocol unreachable"
		case 3:
			cause = "port unreachable"
		case 9, 10, 13:
			cause = "administratively prohibited: blocked by a firewall or ACL"
		}
	} else if t == ipv6.ICMPTypeDestinationUnreachable {
		switch code {
		case 0:
			cause = "no route to destination"
		case 1:
			cause = "administratively prohibited: blocked by a firewall or ACL"
		case 3:
			cause = "address unreachable: target offline or neighbor discovery failed"
		case 4:
			cause = "port unreachable"
		}
	}
	return &pingError{
		msg:   fmt.Sprintf("destination unreachable (code %d) from %s", code, from),
		cause: cause,
	}
}

func peerIP(a net.Addr) string {
	switch v := a.(type) {
	case *net.IPAddr:
		return v.IP.String()
	case *net.UDPAddr:
		return v.IP.String()
	}
	return a.String()
}

// execPinger shells out to the system ping binary.
type execPinger struct {
	bin  string
	host string
}

var pingLineRe = regexp.MustCompile(`from ([^:\s]+).*?time[=<]([\d.]+) ?ms`)

func newExecPinger(host string) (*execPinger, error) {
	bin := "/sbin/ping"
	if _, err := os.Stat(bin); err != nil {
		p, err := exec.LookPath("ping")
		if err != nil {
			return nil, errors.New("raw ICMP not permitted and no ping binary found")
		}
		bin = p
	}
	return &execPinger{bin: bin, host: host}, nil
}

func (p *execPinger) Close() error { return nil }

func (p *execPinger) Ping(ctx context.Context, seq int) (string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	secs := strconv.Itoa(int(pingTimeout.Seconds()))
	var args []string
	switch runtime.GOOS {
	case "darwin", "freebsd", "openbsd", "netbsd":
		args = []string{"-n", "-c", "1", "-t", secs, p.host}
	default:
		args = []string{"-n", "-c", "1", "-W", secs, p.host}
	}
	out, err := exec.CommandContext(ctx, p.bin, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", 0, errTimeout
	}
	if m := pingLineRe.FindSubmatch(out); m != nil {
		ms, perr := strconv.ParseFloat(string(m[2]), 64)
		if perr == nil {
			return string(m[1]), time.Duration(ms * float64(time.Millisecond)), nil
		}
	}
	if err != nil {
		msg := strings.TrimSpace(lastLine(out))
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(strings.ToLower(msg), "100.0% packet loss") || strings.Contains(strings.ToLower(msg), "timeout") {
			return "", 0, errTimeout
		}
		return "", 0, errors.New(msg)
	}
	return "", 0, errors.New("unparseable ping output: " + strings.TrimSpace(lastLine(out)))
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" && !strings.HasPrefix(s, "---") && !strings.HasPrefix(s, "round-trip") && !strings.HasPrefix(s, "rtt") {
			return s
		}
	}
	return ""
}

// monitor pings host every pingInterval until ctx is cancelled, emitting Results on out.
func monitor(ctx context.Context, host string, p pinger, out chan<- Result) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	seq := 0
	for {
		now := time.Now()
		src, lat, err := p.Ping(ctx, seq)
		select {
		case out <- Result{Host: host, Time: now, Seq: seq, SourceIP: src, Latency: lat, Err: err}:
		case <-ctx.Done():
			return
		}
		seq++
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}
