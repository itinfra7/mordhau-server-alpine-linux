package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mordhau-manager/internal/manager"
)

type trustedProxyFlags []netip.Prefix

func (values *trustedProxyFlags) String() string {
	items := make([]string, 0, len(*values))
	for _, prefix := range *values {
		items = append(items, prefix.String())
	}
	return strings.Join(items, ",")
}

func (values *trustedProxyFlags) Set(value string) error {
	prefix, err := manager.ParseTrustedProxy(value)
	if err != nil {
		return err
	}
	for _, existing := range *values {
		if existing == prefix {
			return nil
		}
	}
	*values = append(*values, prefix)
	return nil
}

func main() {
	var trustedProxies trustedProxyFlags
	listen := flag.String("listen", "0.0.0.0:8080", "HTTP listen address")
	initOnly := flag.Bool("init", false, "initialize persistent state and exit")
	recoverDisabledFrom := flag.String(
		"recover-disabled-from",
		"",
		"import matching legacy disabled entries from an INI backup and exit",
	)
	recoverFile := flag.String(
		"recover-file",
		"Game.ini",
		"configuration file used with --recover-disabled-from",
	)
	recoverSection := flag.String(
		"recover-section",
		"",
		"exact INI section used with --recover-disabled-from",
	)
	recoverKey := flag.String(
		"recover-key",
		"",
		"INI key used with --recover-disabled-from",
	)
	flag.Var(
		&trustedProxies,
		"trusted-proxy",
		"trusted reverse-proxy IP address or CIDR prefix; repeat for multiple proxies",
	)
	flag.Parse()

	app, err := manager.New(trustedProxies...)
	if err != nil {
		log.Fatalf("initialize manager: %v", err)
	}
	if *recoverDisabledFrom != "" {
		if *recoverSection == "" || *recoverKey == "" {
			log.Fatal("--recover-section and --recover-key are required for recovery")
		}
		count, err := app.RecoverDisabledEntriesFromBackup(
			*recoverDisabledFrom,
			*recoverFile,
			*recoverSection,
			*recoverKey,
		)
		if err != nil {
			log.Fatalf("recover disabled entries: %v", err)
		}
		fmt.Printf("Recovered %d disabled INI entries.\n", count)
		return
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
