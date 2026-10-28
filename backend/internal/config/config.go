package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
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

	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool
	S3Region    string

	PushSubject       string
	PushAllowInsecure bool

	LiveKitURL       string
	LiveKitAPIURL    string
	LiveKitAPIKey    string
	LiveKitAPISecret string
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
	c.PushSubject = env("PUSH_SUBJECT", "mailto:push@connecta.local")
	if c.PushAllowInsecure, err = strconv.ParseBool(env("PUSH_ALLOW_INSECURE", "false")); err != nil {
		return c, fmt.Errorf("PUSH_ALLOW_INSECURE: %w", err)
	}
	c.LiveKitURL = os.Getenv("LIVEKIT_URL")
	c.LiveKitAPIURL = os.Getenv("LIVEKIT_API_URL")
	c.LiveKitAPIKey = os.Getenv("LIVEKIT_API_KEY")
	c.LiveKitAPISecret = os.Getenv("LIVEKIT_API_SECRET")
	c.S3Endpoint = os.Getenv("S3_ENDPOINT")
	c.S3AccessKey = os.Getenv("S3_ACCESS_KEY")
	c.S3SecretKey = os.Getenv("S3_SECRET_KEY")
	c.S3Bucket = env("S3_BUCKET", "connecta")
	c.S3Region = env("S3_REGION", "us-east-1")
	if c.S3UseSSL, err = strconv.ParseBool(env("S3_USE_SSL", "false")); err != nil {
		return c, fmt.Errorf("S3_USE_SSL: %w", err)
	}

	switch {
	case c.DatabaseURL == "":
		return c, errors.New("DATABASE_URL is required")
	case c.RedisURL == "":
		return c, errors.New("REDIS_URL is required")
	case c.S3Endpoint == "" || c.S3AccessKey == "" || c.S3SecretKey == "":
		return c, errors.New("S3_ENDPOINT, S3_ACCESS_KEY and S3_SECRET_KEY are required")
	case c.LiveKitURL != "" && (c.LiveKitAPIKey == "" || len(c.LiveKitAPISecret) < 32):
		return c, errors.New("LIVEKIT_API_KEY and LIVEKIT_API_SECRET (at least 32 bytes) are required with LIVEKIT_URL")
	case strings.HasPrefix(c.LiveKitURL, "/") && c.LiveKitAPIURL == "":
		return c, errors.New("LIVEKIT_API_URL is required when LIVEKIT_URL is a path")
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
