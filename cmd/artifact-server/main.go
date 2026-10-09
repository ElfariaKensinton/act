package main

import (
	"context"
	"flag"
	"os/signal"
	"syscall"

	"github.com/nektos/act/pkg/artifacts"
)

func main() {
	dir := flag.String("dir", "./artifacts", "directory used to store uploaded artifacts")
	addr := flag.String("addr", "127.0.0.1", "listen address; use a private network or reverse proxy for remote access")
	port := flag.String("port", "8088", "listen port")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	artifacts.Serve(ctx, *dir, *addr, *port)
	<-ctx.Done()
}
