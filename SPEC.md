# Description

The program is a cli for monitoring network stability.

# Context

On a home computer, I would like to have a way of knowing how stable and fast my internet is.
I would want to understand it by proxy of pinging multiple servers at once via ICMP - servers in my
network and outside it, and seeing the resutls all at one screen in one column per monitored server.

# Behavior

The program needs to be started with a list of hosts to monitor on the command line.
It runs indefinitely until stopped with CTRL+C.

It pings the servers in parallel every 5 seconds. The terminal screen is divided into columns per
server, and every ping result results in a small bar shown in the column of the results for the server

The timeout for a ping is 4 seconds.

The bars should look as follows

Ping latencies of 0ms-80ms green bars on a black background:
0 ▁ 10 ▂ 20 ▃ 30 ▄ 40 ▅ 50 ▆ 60 ▇ 70 █ 80 

Latencies of 81ms-160ms orange bars on green background:
81 ▁ 90 ▂ 100 ▃ 110 ▄ 120 ▅ 130 ▆ 140 ▇ 150 █ 160 

Latencies of 161ms - infinity red bars on orange background:
161 ▁ 170 ▂ 180 ▃ 190 ▄ 200 ▅ 210 ▆ 220 ▇ 230 █ ∞

If there is a failure of the ping or a 4 seconds timeout, a red ! on a black background should be 
printed.

Additionally, I would like that the monitoring results are logged in an OpenTelemetry compatible
format per monitored server. Every log entry must be a one-liner. It must always include the time 
of the ping request in ISO 8601. If the response is successful, the one-liner should have the IP from which the 
ICMP request appears to have happened, the icmp sequence number, and the time latency in ms.
If the request failed, the log entry should provide the exact error and probable cause of the failure.
If log files for a host exist already, they should be appended to, instead of overwritten.

# Examples

running `conntest 8.8.8.8 192.168.1.1 127.0.0.1` should produce a following display after 10 pings:

```
| 8.8.8.8          | 192.168.1.1      | 127.0.0.1        |
|------------------|------------------|------------------|
| ▄▆▁!▃▃▃▃█▄       | ▃▃▃▃▃▃▃▃▃▃       | ▁▁▁▁▁▁▁▁▁▁       |
```

The line wraps to a new line when the bars of one of the monitored hosts reaches the column width

and there should be three files: 8.8.8.8.ctlog, 192.168.1.1.ctlog, and 127.0.0.1.ctlog

In the file 8.8.8.8.ctlog, there should be a following content
```
{"time":"2026-11-04T15:44:00+01:00", "target":"8.8.8.8", "source_ip":"142.250.254.101", "icmp_seq":0, "latency_ms":35}
{"time":"2026-11-04T15:44:05+01:00", "target":"8.8.8.8", "source_ip":"142.250.254.101", "icmp_seq":1, "latency_ms":53}
{"time":"2026-11-04T15:44:10+01:00", "target":"8.8.8.8", "source_ip":"142.250.254.101", "icmp_seq":2, "latency_ms":10}
{"time":"2026-11-04T15:44:15+01:00", "target":"8.8.8.8", "source_ip":"142.250.254.101", "icmp_seq":3, "error":"timeout after 4 seconds"}
{"time":"2026-11-04T15:44:20+01:00", "target":"8.8.8.8", "source_ip":"142.250.254.101", "icmp_seq":4, "latency_ms":22}
{"time":"2026-11-04T15:44:25+01:00", "target":"8.8.8.8", "source_ip":"142.250.254.101", "icmp_seq":5, "latency_ms":23}
{"time":"2026-11-04T15:44:30+01:00", "target":"8.8.8.8", "source_ip":"142.250.254.101", "icmp_seq":6, "latency_ms":29}
{"time":"2026-11-04T15:44:35+01:00", "target":"8.8.8.8", "source_ip":"142.250.254.101", "icmp_seq":7, "latency_ms":30}
{"time":"2026-11-04T15:44:40+01:00", "target":"8.8.8.8", "source_ip":"142.250.254.101", "icmp_seq":8, "latency_ms":71}
{"time":"2026-11-04T15:44:45+01:00", "target":"8.8.8.8", "source_ip":"142.250.254.101", "icmp_seq":9, "latency_ms":35}
```

In the other files there should be 10 logs accordingly too.

# Technical notes

Use golang without a full-fledged TUI library unless absolutely necessary.
Try to organize the codebase in 4 files in the root of the project:
main.go      – arg parsing, signal handling, wiring
pinger.go    – ICMP echo per host, seq counter, 4s timeout
display.go   – terminal grid renderer
ctlog.go     – JSON-lines logger per host (append mode)
Do fallback to `/sbin/ping` when raw sockets aren't permitted
