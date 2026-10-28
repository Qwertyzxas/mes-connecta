package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"connecta/internal/api"
	"connecta/internal/auth"
	"connecta/internal/calls"
	"connecta/internal/config"
	"connecta/internal/files"
	"connecta/internal/push"
	"connecta/internal/ratelimit"
	"connecta/internal/realtime"
	"connecta/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := store.Connect(ctx, cfg.DatabaseURL, 30*time.Second)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	if err := store.Migrate(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("redis url: %w", err)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: %w", err)
	}

	storage, err := files.Open(ctx, files.Config{
		Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey,
		Bucket: cfg.S3Bucket, UseSSL: cfg.S3UseSSL, Region: cfg.S3Region,
	})
	if err != nil {
		return fmt.Errorf("object storage: %w", err)
	}

	hub, err := realtime.NewHub(ctx, rdb, log)
	if err != nil {
		return fmt.Errorf("realtime hub: %w", err)
	}
	defer hub.Close()

	st := store.New(pool)
	presence := realtime.NewPresence(rdb)
	pusher, err := push.New(ctx, st, log, presence.Online, push.Config{
		Subject: cfg.PushSubject, AllowInsecure: cfg.PushAllowInsecure,
	})
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}

	srv := api.New(
		cfg,
		st,
		auth.NewTokens(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTTL),
		ratelimit.New(rdb),
		rdb,
		hub,
		storage,
		pusher,
		calls.New(calls.Config{
			URL: cfg.LiveKitURL, APIURL: cfg.LiveKitAPIURL,
			APIKey: cfg.LiveKitAPIKey, APISecret: cfg.LiveKitAPISecret,
		}),
		log,
	)
	go srv.RunCallJanitor(ctx)

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}
