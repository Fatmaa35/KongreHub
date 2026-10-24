// Package config reads all configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL          string
	HTTPAddr             string
	JWTSecret            string
	AccessTTL            time.Duration
	RefreshTTL           time.Duration
	CertSigningSeed      string
	WebhookSecret        string
	PublicURL            string
	StorageDir           string
	DemoMode             bool
	JobInterval          time.Duration
	RequestTimeout       time.Duration
	MaxUploadBytes       int64
	CORSOrigins          []string
	LogLevel             string
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func dur(k, def string) time.Duration {
	d, err := time.ParseDuration(env(k, def))
	if err != nil {
		panic(fmt.Sprintf("config: %s geçersiz süre: %v", k, err))
	}
	return d
}

// Load reads the environment and validates required secrets.
func Load() (Config, error) {
	mb, _ := strconv.Atoi(env("MAX_UPLOAD_MB", "10"))
	c := Config{
		DatabaseURL:     env("DATABASE_URL", "postgres://kongrehub:kongrehub@localhost:5432/kongrehub?sslmode=disable"),
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		JWTSecret:       env("JWT_SECRET", ""),
		AccessTTL:       dur("ACCESS_TOKEN_TTL", "15m"),
		RefreshTTL:      dur("REFRESH_TOKEN_TTL", "168h"),
		CertSigningSeed: env("CERT_SIGNING_SEED", ""),
		WebhookSecret:   env("PAYMENT_WEBHOOK_SECRET", ""),
		PublicURL:       env("PUBLIC_URL", "http://localhost:3000"),
		StorageDir:      env("STORAGE_DIR", "./data/uploads"),
		DemoMode:        env("DEMO_MODE", "false") == "true",
		JobInterval:     dur("JOB_INTERVAL", "1m"),
		RequestTimeout:  dur("REQUEST_TIMEOUT", "30s"),
		MaxUploadBytes:  int64(mb) << 20,
		CORSOrigins:     strings.Split(env("CORS_ORIGINS", "http://localhost:3000,http://localhost:5173"), ","),
		LogLevel:        env("LOG_LEVEL", "info"),
	}
	if len(c.JWTSecret) < 32 {
		return c, fmt.Errorf("JWT_SECRET en az 32 karakter olmalı")
	}
	if c.CertSigningSeed == "" || c.WebhookSecret == "" {
		return c, fmt.Errorf("CERT_SIGNING_SEED ve PAYMENT_WEBHOOK_SECRET zorunlu")
	}
	return c, nil
}
