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

func jsonErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func reqID() string {
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), rand.Uint32())
}

// Handler is the Vercel serverless entry point for proxy requests.
// Expects route: /proxy/:path* → /api/index?path=:path*
func Handler(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if p := recover(); p != nil {
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("panic: %v", p))
		}
	}()

	c, err := loadCfg()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// auth
	if _, ok := c.apiKeys[r.Header.Get("X-API-Key")]; !ok {
		jsonErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	target, _ := url.Parse(c.downstreamURL)

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{
		ResponseHeaderTimeout: c.requestTimeout,
	}

	proxy.Director = func(req *http.Request) {
		// Vercel passes captured :path* as query param when using
		// destination "/api/index?path=:path*"
		upstreamPath := "/" + req.URL.Query().Get("path")

		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = upstreamPath
		req.URL.RawQuery = "" // remove the internal ?path= param
		req.Host = target.Host

		req.Header.Del("X-API-Key")
		req.Header.Set("X-API-Key", c.downstreamAPIKey)
		if req.Header.Get("X-Request-ID") == "" {
			req.Header.Set("X-Request-ID", reqID())
		}
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		jsonErr(w, http.StatusBadGateway, "bad gateway")
	}

	proxy.ServeHTTP(w, r)
}
