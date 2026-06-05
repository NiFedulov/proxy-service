package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Anthropic API request format
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []block
}

// Anthropic API response format
type anthropicResponse struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	Role         string            `json:"role"`
	Model        string            `json:"model"`
	Content      []anthropicBlock  `json:"content"`
	StopReason   string            `json:"stop_reason"`
	StopSequence *string           `json:"stop_sequence"`
	Usage        anthropicUsage    `json:"usage"`
}

type anthropicBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// extractText pulls the text content from the last user message.
func extractText(msgs []anthropicMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "user" {
			continue
		}
		switch v := m.Content.(type) {
		case string:
			return v
		case []any:
			var parts []string
			for _, item := range v {
				if block, ok := item.(map[string]any); ok {
					if block["type"] == "text" {
						if t, ok := block["text"].(string); ok {
							parts = append(parts, t)
						}
					}
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

// POST /send/messages  — Anthropic-compatible endpoint routed through local bridge.
// Chatbox sets API Host = https://proxy-service-red.vercel.app/send
// and calls /send/messages automatically.
func sendMessagesHandler(w http.ResponseWriter, r *http.Request) {
	var req anthropicRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResp(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	message := extractText(req.Messages)
	if message == "" {
		jsonResp(w, http.StatusBadRequest, map[string]string{"error": "no user message found"})
		return
	}

	// build prompt with full conversation context if multiple messages
	if len(req.Messages) > 1 {
		var parts []string
		for _, m := range req.Messages {
			text := ""
			switch v := m.Content.(type) {
			case string:
				text = v
			}
			parts = append(parts, fmt.Sprintf("%s: %s", m.Role, text))
		}
		message = strings.Join(parts, "\n")
	}

	// enqueue job
	id := newID()
	j := &job{
		ID:      id,
		Message: message,
		Status:  statusPending,
		created: time.Now(),
		done:    make(chan struct{}),
	}
	mu.Lock()
	jobsMap[id] = j
	mu.Unlock()

	select {
	case pending <- id:
	default:
		mu.Lock()
		delete(jobsMap, id)
		mu.Unlock()
		jsonResp(w, http.StatusServiceUnavailable, map[string]string{"error": "bridge queue full"})
		return
	}

	// wait for local bridge to respond (60s timeout)
	select {
	case <-j.done:
	case <-time.After(60 * time.Second):
		jsonResp(w, http.StatusGatewayTimeout, map[string]string{
			"error": "bridge did not respond in time — make sure bridge is running locally",
		})
		return
	}

	mu.RLock()
	response := j.Response
	errMsg := j.Error
	model := req.Model
	mu.RUnlock()

	if errMsg != "" {
		jsonResp(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}

	// return Anthropic-compatible response
	jsonResp(w, http.StatusOK, anthropicResponse{
		ID:         "msg_bridge_" + id,
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    []anthropicBlock{{Type: "text", Text: strings.TrimSpace(response)}},
		StopReason: "end_turn",
		Usage:      anthropicUsage{},
	})
}
