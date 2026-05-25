//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func installShutdownSignalHandler(cleanup func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		cleanup()
		os.Exit(0)
	}()
}
