package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	ListenAddr        string
	DownstreamURL     string
	DownstreamAPIKey  string
	APIKeys           map[string]struct{}
	RequestTimeout    time.Duration
}

func LoadConfig() (Config, error) {
	downstreamURL := os.Getenv("DOWNSTREAM_URL")
	if downstreamURL == "" {
		return Config{}, fmt.Errorf("DOWNSTREAM_URL is required")
	}

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}

	requestTimeout := 30 * time.Second
	if v := os.Getenv("REQUEST_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid REQUEST_TIMEOUT: %w", err)
		}
		requestTimeout = d
	}

	apiKeys := make(map[string]struct{})
	if raw := os.Getenv("API_KEYS"); raw != "" {
		for _, k := range strings.Split(raw, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				apiKeys[k] = struct{}{}
			}
		}
	}
	if len(apiKeys) == 0 {
		return Config{}, fmt.Errorf("API_KEYS is required and must contain at least one key")
	}

	downstreamAPIKey := os.Getenv("DOWNSTREAM_API_KEY")
	if downstreamAPIKey == "" {
		return Config{}, fmt.Errorf("DOWNSTREAM_API_KEY is required")
	}

	return Config{
		ListenAddr:       listenAddr,
		DownstreamURL:    downstreamURL,
		DownstreamAPIKey: downstreamAPIKey,
		APIKeys:          apiKeys,
		RequestTimeout:   requestTimeout,
	}, nil
}
