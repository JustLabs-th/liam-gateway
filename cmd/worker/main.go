package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"llm-gateway/internal/app"
	"llm-gateway/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	path := flag.String("config", "configs/local.yaml", "configuration file")
	flag.Parse()
	if e := run(*path); e != nil {
		slog.Error("worker stopped", "error", e)
		os.Exit(1)
	}
}
func run(path string) error {
	c, e := config.Load(path)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rt, e := app.Open(ctx, c)
	if e != nil {
		return e
	}
	defer rt.Close()
	w := app.Worker{Store: rt.Store, Archive: rt.Archive, Config: c}
	w.Run(ctx)
	return nil
}
