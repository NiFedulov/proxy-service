package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"
)

type claudeRequest struct {
	Message string `json:"message"`
}

type claudeResponse struct {
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
}

// NewClaudeHandler runs incoming messages through the local `claude -p` CLI
// and returns the output as a JSON response.
func NewClaudeHandler(timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req claudeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(claudeResponse{Error: "body must be JSON {\"message\": \"...\"}"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "claude", "-p", req.Message)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				w.WriteHeader(http.StatusGatewayTimeout)
				json.NewEncoder(w).Encode(claudeResponse{Error: "claude CLI timed out"})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(claudeResponse{Error: stderr.String()})
			return
		}

		json.NewEncoder(w).Encode(claudeResponse{Response: stdout.String()})
	})
}
