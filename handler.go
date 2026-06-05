package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func NewProxyHandler(cfg Config) (http.Handler, error) {
	target, err := url.Parse(cfg.DownstreamURL)
	if err != nil {
		return nil, fmt.Errorf("invalid DOWNSTREAM_URL: %w", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	proxy.Transport = &http.Transport{
		ResponseHeaderTimeout: cfg.RequestTimeout,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
	}

	defaultDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		defaultDirector(req)

		// don't leak the client's API key to downstream
		req.Header.Del("X-API-Key")

		// authenticate the proxy to downstream
		req.Header.Set("X-API-Key", cfg.DownstreamAPIKey)

		if req.Header.Get("X-Request-ID") == "" {
			req.Header.Set("X-Request-ID", newRequestID())
		}

		req.Host = target.Host
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Error("downstream error",
			"request_id", r.Header.Get("X-Request-ID"),
			"error", err,
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": "bad gateway"})
	}

	return proxy, nil
}

func newRequestID() string {
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), rand.Uint32())
}
