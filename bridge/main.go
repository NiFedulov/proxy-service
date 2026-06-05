package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"time"
)

func main() {
	serverURL := getenv("SERVER_URL", "https://proxy-service-red.vercel.app")
	apiKey := getenv("API_KEY", "proxy-7f3a9d2e1b8c4f6a")
	claudeTimeout := 120 * time.Second

	slog.Info("bridge started", "server", serverURL)

	client := &http.Client{Timeout: 15 * time.Second}

	for {
		job, ok := poll(client, serverURL, apiKey)
		if !ok {
			continue
		}

		slog.Info("received job", "id", job.ID, "message", job.Message)

		result := runClaude(job.Message, claudeTimeout)
		if result.Error != "" {
			slog.Error("claude error", "id", job.ID, "error", result.Error)
		} else {
			slog.Info("claude done", "id", job.ID)
		}

		submit(client, serverURL, apiKey, job.ID, result)
	}
}

type job struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type result struct {
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
}

func poll(client *http.Client, serverURL, apiKey string) (job, bool) {
	req, _ := http.NewRequest("GET", serverURL+"/poll", nil)
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("poll error", "error", err)
		time.Sleep(3 * time.Second)
		return job{}, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return job{}, false // no pending jobs
	}
	if resp.StatusCode != http.StatusOK {
		slog.Error("poll unexpected status", "status", resp.StatusCode)
		time.Sleep(3 * time.Second)
		return job{}, false
	}

	var j job
	json.NewDecoder(resp.Body).Decode(&j)
	return j, j.ID != ""
}

func runClaude(message string, timeout time.Duration) result {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "claude", "-p", message)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return result{Error: "claude timed out"}
		}
		msg := stderr.String()
		if msg == "" {
			msg = err.Error()
		}
		return result{Error: msg}
	}

	return result{Response: stdout.String()}
}

func submit(client *http.Client, serverURL, apiKey, jobID string, r result) {
	body, _ := json.Marshal(r)
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/done/%s", serverURL, jobID), bytes.NewReader(body))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("submit error", "id", jobID, "error", err)
		return
	}
	resp.Body.Close()
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
