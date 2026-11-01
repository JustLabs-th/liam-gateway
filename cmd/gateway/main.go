package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"llm-gateway/internal/app"
	"llm-gateway/internal/config"
	"llm-gateway/internal/identity"
	"llm-gateway/internal/telemetry"
	httptransport "llm-gateway/internal/transport/http"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if e := run(); e != nil {
		slog.Error("gateway stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "configs/local.yaml", "configuration file")
	flag.Parse()
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	shutdownTrace, e := telemetry.Start(context.Background())
	if e != nil {
		return e
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTrace(ctx)
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	rt, e := app.Open(startup, c)
	if e != nil {
		cancel()
		return e
	}
	defer rt.Close()
	auth, e := identity.New(startup, c, rt.Store)
	cancel()
	if e != nil {
		return e
	}
	server := &http.Server{Addr: c.Listen, Handler: httptransport.Router(c, rt.Store, rt.Quota, auth, rt.Archive), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	// Per-request deadlines belong to inference; a global WriteTimeout would cut off SSE.
	done := make(chan error, 1)
	go func() {
		slog.Info("gateway listening", "address", c.Listen, "auth_mode", c.Auth.Mode)
		done <- server.ListenAndServe()
	}()
	select {
	case e := <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e = server.Shutdown(shutdown); e != nil {
		_ = server.Close()
		return e
	}
	return nil
}
