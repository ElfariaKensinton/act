package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nektos/act/pkg/artifacts"
)

func main() {
	dir := flag.String("dir", "./artifacts", "directory used to store uploaded artifacts")
	addr := flag.String("addr", "127.0.0.1", "listen address; use a private network or reverse proxy for remote access")
	port := flag.String("port", "8088", "listen port")
	retentionDays := flag.Int("retention-days", 90, "fallback artifact lifetime in days when the upload supplies no expiration (1-400)")
	flag.Parse()
	if *retentionDays < 1 || *retentionDays > 400 {
		fmt.Fprintln(os.Stderr, "--retention-days must be between 1 and 400")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cancel := artifacts.Serve(ctx, *dir, *addr, *port, artifacts.ServeOptions{RetentionDays: *retentionDays})
	<-ctx.Done()
	cancel()
}
