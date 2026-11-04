package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	hosts := os.Args[1:]
	if len(hosts) == 0 {
		fmt.Fprintln(os.Stderr, "usage: conntest HOST [HOST...]")
		os.Exit(2)
	}
	if err := run(hosts); err != nil {
		fmt.Fprintln(os.Stderr, "conntest:", err)
		os.Exit(1)
	}
}

func run(hosts []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pingers := make(map[string]pinger, len(hosts))
	loggers := make(map[string]*ctLogger, len(hosts))
	defer func() {
		for _, p := range pingers {
			p.Close()
		}
		for _, l := range loggers {
			l.Close()
		}
	}()
	for _, h := range hosts {
		p, err := newPinger(h)
		if err != nil {
			return fmt.Errorf("%s: %w", h, err)
		}
		pingers[h] = p
		l, err := openCTLog(h)
		if err != nil {
			return err
		}
		loggers[h] = l
	}

	results := make(chan Result, len(hosts))
	done := make(chan struct{}, len(hosts))
	for _, h := range hosts {
		go func(h string) {
			monitor(ctx, h, pingers[h], results)
			done <- struct{}{}
		}(h)
	}

	d := newDisplay(os.Stdout, hosts)
	d.Start()
	defer d.Stop()

	running := len(hosts)
	for running > 0 {
		select {
		case r := <-results:
			d.Add(r)
			if err := loggers[r.Host].Log(r); err != nil {
				return err
			}
		case <-done:
			running--
		}
	}
	return nil
}
