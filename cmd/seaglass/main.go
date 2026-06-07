package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"seaglass/bundler"
	"seaglass/web"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

var (
	host  string
	port  int
	build bool
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: func() slog.Level {
			switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
			case "debug":
				return slog.LevelDebug
			case "info":
				return slog.LevelInfo
			case "warn":
				return slog.LevelWarn
			case "error":
				return slog.LevelError
			default:
				return slog.LevelInfo
			}
		}(),
	}))
	slog.SetDefault(logger)

	flag.StringVar(&host, "h", "127.0.0.1", "")
	flag.IntVar(&port, "p", 8080, "")
	flag.BoolVar(&build, "build", false, "one-shot production build and exit")
	flag.Parse()

	if build {
		if err := bundler.Build(&bundler.BuildConfig{Directory: web.ResourcesDirectoryPath}); err != nil {
			slog.Error("build failed", "error", err)
			os.Exit(1)
		}
		return
	}

	ctx := context.Background()
	if err := run(ctx); err != nil {
		slog.Error("server failure", "error", err)
		os.Exit(1)
	}
	slog.Info("shutdown complete")
}

func run(ctx context.Context) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	mux := http.NewServeMux()
	if err := setupMux(mux); err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
		BaseContext: func(l net.Listener) context.Context {
			return ctx
		},
	}

	bcfg := &bundler.BuildConfig{
		Addr:         addr,
		Endpoint:     ReloadTriggerEndpoint,
		Directory:    web.ResourcesDirectoryPath,
		ShouldReload: true,
	}

	if err := bundler.Build(bcfg); err != nil {
		slog.Error("initial build failed", "error", err)
	}

	g, egctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return bundler.Watch(egctx, bcfg)
	})

	g.Go(func() (err error) {
		slog.Info("server started", slog.String("addr", srv.Addr))
		if err = srv.ListenAndServe(); err == nil || err == http.ErrServerClosed {
			return nil
		}
		return err
	})

	g.Go(func() (err error) {
		<-egctx.Done()
		slog.Debug("shutdown signal received")

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		return srv.Shutdown(shutdownCtx)
	})

	return g.Wait()
}
