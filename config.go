package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr               string
	BaseURL                *url.URL
	DatabaseURL            string
	OIDCIssuer             string
	OIDCClientID           string
	OIDCClientSecret       string
	OIDCRedirectURL        string
	SessionTTL             time.Duration
	AllowedOrigins         map[string]struct{}
	CookieSecure           bool
	TrustProxyHeaders      bool
	MaxMessageBytes        int
	MaxConversationMembers int
}

func LoadConfig() (Config, error) {
	baseURL, err := url.Parse(env("CHAT_BASE_URL", "http://localhost:8080"))
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return Config{}, errors.New("CHAT_BASE_URL must be an absolute URL")
	}

	ttl, err := time.ParseDuration(env("SESSION_TTL", "24h"))
	if err != nil || ttl <= 0 {
		return Config{}, errors.New("SESSION_TTL must be a positive duration")
	}

	maxMessageBytes, err := strconv.Atoi(env("MAX_MESSAGE_BYTES", "8192"))
	if err != nil || maxMessageBytes < 1 {
		return Config{}, errors.New("MAX_MESSAGE_BYTES must be a positive integer")
	}
	maxConversationMembers, err := strconv.Atoi(env("MAX_CONVERSATION_MEMBERS", "1000"))
	if err != nil || maxConversationMembers < 2 {
		return Config{}, errors.New("MAX_CONVERSATION_MEMBERS must be at least 2")
	}
	trustProxyHeaders, err := strconv.ParseBool(env("TRUST_PROXY_HEADERS", "false"))
	if err != nil {
		return Config{}, errors.New("TRUST_PROXY_HEADERS must be true or false")
	}

	origins := make(map[string]struct{})
	configuredOrigins := env("ALLOWED_ORIGINS", baseURL.Scheme+"://"+baseURL.Host)
	for _, origin := range strings.Split(configuredOrigins, ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			origins[origin] = struct{}{}
		}
	}

	cfg := Config{
		HTTPAddr:               env("HTTP_ADDR", ":8080"),
		BaseURL:                baseURL,
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		OIDCIssuer:             env("OIDC_ISSUER", "https://my.lsong.org"),
		OIDCClientID:           os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:       os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:        env("OIDC_REDIRECT_URL", baseURL.ResolveReference(&url.URL{Path: "/auth/callback"}).String()),
		SessionTTL:             ttl,
		AllowedOrigins:         origins,
		CookieSecure:           baseURL.Scheme == "https",
		TrustProxyHeaders:      trustProxyHeaders,
		MaxMessageBytes:        maxMessageBytes,
		MaxConversationMembers: maxConversationMembers,
	}

	var missing []string
	for name, value := range map[string]string{
		"DATABASE_URL":       cfg.DatabaseURL,
		"OIDC_CLIENT_ID":     cfg.OIDCClientID,
		"OIDC_CLIENT_SECRET": cfg.OIDCClientSecret,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
