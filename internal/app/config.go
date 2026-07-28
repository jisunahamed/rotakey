package app

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr                string
	DatabaseURL         string
	RedisURL            string
	MasterKey           []byte
	BootstrapToken      string
	PublicBaseURL       string
	SessionCookieSecure bool
	MaxRequestBytes     int64
	MaxResponseBytes    int64
	CaptureBytes        int64
	SessionTTL          time.Duration
}

func LoadConfig() (Config, error) {
	cfg := Config{
		Addr:                envOr("APP_ADDR", ":8080"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		RedisURL:            envOr("REDIS_URL", "redis://redis:6379/0"),
		BootstrapToken:      os.Getenv("BOOTSTRAP_TOKEN"),
		PublicBaseURL:       strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),
		SessionCookieSecure: envBool("SESSION_COOKIE_SECURE", false),
		MaxRequestBytes:     envInt64("MAX_REQUEST_BYTES", 8<<20),
		MaxResponseBytes:    envInt64("MAX_RESPONSE_BYTES", 16<<20),
		CaptureBytes:        envInt64("CAPTURE_BYTES", 1<<20),
		SessionTTL:          time.Duration(envInt64("SESSION_TTL_HOURS", 12)) * time.Hour,
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if len(cfg.BootstrapToken) < 24 {
		return Config{}, errors.New("BOOTSTRAP_TOKEN must be at least 24 characters")
	}

	rawKey := os.Getenv("APP_MASTER_KEY")
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil || len(key) != 32 {
		return Config{}, errors.New("APP_MASTER_KEY must be a base64-encoded 32-byte key")
	}
	cfg.MasterKey = key

	return cfg, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(name string, fallback int64) int64 {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func (c Config) ValidateProduction() error {
	if strings.HasPrefix(c.PublicBaseURL, "https://") && !c.SessionCookieSecure {
		return fmt.Errorf("SESSION_COOKIE_SECURE must be true when PUBLIC_BASE_URL uses HTTPS")
	}
	return nil
}
