package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr     string
	DatabaseURL  string
	RedisURL     string
	JWTSecret    []byte
	JWTIssuer    string
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	CookieSecure bool
	TrustProxy   bool
}

func Load() (Config, error) {
	c := Config{
		HTTPAddr:    env("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		JWTSecret:   []byte(os.Getenv("JWT_SECRET")),
		JWTIssuer:   env("JWT_ISSUER", "connecta"),
	}

	var err error
	if c.AccessTTL, err = time.ParseDuration(env("ACCESS_TTL", "15m")); err != nil {
		return c, fmt.Errorf("ACCESS_TTL: %w", err)
	}
	if c.RefreshTTL, err = time.ParseDuration(env("REFRESH_TTL", "720h")); err != nil {
		return c, fmt.Errorf("REFRESH_TTL: %w", err)
	}
	if c.CookieSecure, err = strconv.ParseBool(env("COOKIE_SECURE", "true")); err != nil {
		return c, fmt.Errorf("COOKIE_SECURE: %w", err)
	}
	if c.TrustProxy, err = strconv.ParseBool(env("TRUST_PROXY", "false")); err != nil {
		return c, fmt.Errorf("TRUST_PROXY: %w", err)
	}

	switch {
	case c.DatabaseURL == "":
		return c, errors.New("DATABASE_URL is required")
	case c.RedisURL == "":
		return c, errors.New("REDIS_URL is required")
	case len(c.JWTSecret) < 32:
		return c, errors.New("JWT_SECRET must be at least 32 bytes")
	case c.AccessTTL <= 0 || c.RefreshTTL <= c.AccessTTL:
		return c, errors.New("REFRESH_TTL must be greater than ACCESS_TTL")
	}
	return c, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
