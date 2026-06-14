package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"tangled.org/core/idresolver"
	"tangled.org/core/notifier"
	"tangled.org/core/xrpc/serviceauth"

	"github.com/isabelroses/snot/internal/config"
	"github.com/isabelroses/snot/internal/events"
	"github.com/isabelroses/snot/internal/forgejo"
	"github.com/isabelroses/snot/internal/gitserve"
	"github.com/isabelroses/snot/internal/repodid"
	"github.com/isabelroses/snot/internal/resolve"
	"github.com/isabelroses/snot/internal/state"
	"github.com/isabelroses/snot/internal/webhook"
	"github.com/isabelroses/snot/internal/xrpc"
)

type ServeCmd struct{}

func (c *ServeCmd) Run(cli *CLI) error {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.Load(ctx, cli.Config)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	store, err := forgejo.NewPostgres(ctx, cfg.DbDsn)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer store.Close()

	db, err := state.Open(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	rkeys := db.Rkeys()
	dids := db.RepoDids()

	eventStore, err := db.SQL()
	if err != nil {
		return fmt.Errorf("event store: %w", err)
	}

	scheme := "https"
	if cfg.Dev {
		scheme = "http"
	}
	base := scheme + "://" + cfg.Hostname

	rs := &resolve.Resolver{
		Cfg:   cfg,
		Store: store,
		Rkeys: rkeys,
		Dids:  dids,
		MintDid: func(ctx context.Context) (string, []byte, error) {
			return repodid.Mint(ctx, cfg.PlcUrl, base)
		},
	}

	resolver := idresolver.DefaultResolver(cfg.PlcUrl)

	x := &xrpc.Xrpc{
		Cfg:     cfg,
		Store:   store,
		Resolve: rs,
		Rkeys:   rkeys,
		Logger:  logger.With("component", "xrpc"),
		ServiceAuth: serviceauth.NewServiceAuth(
			logger.With("component", "serviceauth"),
			resolver,
			serviceauth.DidWeb(cfg.Hostname).String(),
		),
	}

	gs := &gitserve.Handler{
		Cfg:      cfg,
		Resolve:  rs,
		Logger:   logger.With("component", "gitserve"),
		Resolver: resolver,
	}

	n := notifier.New()

	wh := &webhook.Handler{
		Cfg:      cfg,
		Resolve:  rs,
		Store:    eventStore,
		Notifier: &n,
		Logger:   logger.With("component", "webhook"),
	}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(loggingMiddleware(logger))
	r.Use(corsMiddleware)

	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "this is snot, a tangled knot backed by forgejo.\n")
	})
	r.Mount("/xrpc", x.Router())
	r.Get("/events", events.Handler(eventStore, &n, logger.With("component", "events")))
	r.Post("/hooks/forgejo", wh.ServeHTTP)
	r.Mount("/", gs.Routes())

	logger.Info("starting snot", "addr", cfg.ListenAddr, "hostname", cfg.Hostname)
	return http.ListenAndServe(cfg.ListenAddr, r)
}

func loggingMiddleware(l *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			l.Info("request", "method", r.Method, "path", r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
