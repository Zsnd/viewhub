package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	"viewhub/apps/capture"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	address := flag.String("listen", "127.0.0.1:18491", "loopback HTTP address")
	config := flag.String("config", filepath.Join(dir, "ViewHubCapture", "config.json"), "private config file")
	web := flag.String("web", "apps/capture/web/dist", "built web directory")
	flag.Parse()
	host, _, err := net.SplitHostPort(*address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("listen 必须是回环 IP 地址")
	}
	device, err := capture.NewDevice(*config)
	if err != nil {
		return err
	}
	defer device.Stop()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Addr: *address, Handler: device.Handler(*web), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		timeout, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		server.Shutdown(timeout)
	}()
	fmt.Println("ViewHub Capture: http://" + *address)
	err = server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
