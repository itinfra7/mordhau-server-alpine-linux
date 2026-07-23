package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mordhau-manager/internal/manager"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:8080", "HTTP listen address")
	initOnly := flag.Bool("init", false, "initialize persistent state and exit")
	flag.Parse()

	app, err := manager.New()
	if err != nil {
		log.Fatalf("initialize manager: %v", err)
	}
	if *initOnly {
		fmt.Println("MORDHAU manager state initialized.")
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app.StartBackground(ctx)

	server := &http.Server{
		Addr:              *listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	listener, err := net.Listen("tcp4", *listen)
	if err != nil {
		log.Fatalf("listen on %s: %v", *listen, err)
	}
	log.Printf("MORDHAU manager listening on %s", listener.Addr())
	err = server.Serve(listener)
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("web server: %v", err)
	}
}
