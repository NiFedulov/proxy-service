package handler

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

type cfg struct {
	downstreamURL    string
	downstreamAPIKey string
	apiKeys          map[string]struct{}
	requestTimeout   time.Duration
}

func loadCfg() (cfg, error) {
	downstreamURL := os.Getenv("DOWNSTREAM_URL")
	if downstreamURL == "" {
		return cfg{}, fmt.Errorf("DOWNSTREAM_URL is required")
	}

	downstreamAPIKey := os.Getenv("DOWNSTREAM_API_KEY")
	if downstreamAPIKey == "" {
		return cfg{}, fmt.Errorf("DOWNSTREAM_API_KEY is required")
	}

	apiKeys := make(map[string]struct{})
	for _, k := range strings.Split(os.Getenv("API_KEYS"), ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			apiKeys[k] = struct{}{}
		}
	}
	if len(apiKeys) == 0 {
		return cfg{}, fmt.Errorf("API_KEYS is required")
	}

	timeout := 30 * time.Second
	if v := os.Getenv("REQUEST_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}

	return cfg{
		downstreamURL:    downstreamURL,
		downstreamAPIKey: downstreamAPIKey,
		apiKeys:          apiKeys,
		requestTimeout:   timeout,
	}, nil
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func requestID() string {
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), rand.Uint32())
}

// Handler is the Vercel serverless entry point.
func Handler(w http.ResponseWriter, r *http.Request) {
	// health check — no auth required
	if r.URL.Path == "/health" {
		w.WriteHeader(http.StatusOK)
		return
	}

	c, err := loadCfg()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// auth
	key := r.Header.Get("X-API-Key")
	if _, ok := c.apiKeys[key]; !ok {
		jsonError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	target, err := url.Parse(c.downstreamURL)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "invalid downstream URL")
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{
		ResponseHeaderTimeout: c.requestTimeout,
	}

	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		// strip /proxy prefix if present
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/proxy")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.Host = target.Host
		req.Header.Del("X-API-Key")
		req.Header.Set("X-API-Key", c.downstreamAPIKey)
		if req.Header.Get("X-Request-ID") == "" {
			req.Header.Set("X-Request-ID", requestID())
		}
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		jsonError(w, http.StatusBadGateway, "bad gateway")
	}

	proxy.ServeHTTP(w, r)
}
