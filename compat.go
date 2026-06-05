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
	Stream    bool               `json:"stream"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []block
}

// Anthropic API response format (non-streaming)
type anthropicResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []anthropicBlock `json:"content"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        anthropicUsage `json:"usage"`
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

// sseEvent writes a single SSE data line.
func sseEvent(w http.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// POST /send/messages — Anthropic-compatible endpoint routed through local bridge.
// Chatbox: set API Host = https://proxy-service-red.vercel.app/send
func sendMessagesHandler(w http.ResponseWriter, r *http.Request) {
	var req anthropicRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResp(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	// build message text (include conversation history)
	var message string
	if len(req.Messages) == 1 {
		message = extractText(req.Messages)
	} else {
		var parts []string
		for _, m := range req.Messages {
			text := extractText([]anthropicMessage{m})
			if text != "" {
				parts = append(parts, fmt.Sprintf("%s: %s", m.Role, text))
			}
		}
		message = strings.Join(parts, "\n")
	}
	if message == "" {
		jsonResp(w, http.StatusBadRequest, map[string]string{"error": "no user message found"})
		return
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

	// wait for local bridge (60s)
	select {
	case <-j.done:
	case <-time.After(60 * time.Second):
		jsonResp(w, http.StatusGatewayTimeout, map[string]string{
			"error": "bridge did not respond — make sure bridge is running locally",
		})
		return
	}

	mu.RLock()
	response := strings.TrimSpace(j.Response)
	errMsg := j.Error
	model := req.Model
	mu.RUnlock()

	if errMsg != "" {
		jsonResp(w, http.StatusBadGateway, map[string]string{"error": errMsg})
		return
	}

	msgID := "msg_bridge_" + id

	if req.Stream {
		// SSE streaming response (Chatbox default)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		sseEvent(w, map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": msgID, "type": "message", "role": "assistant",
				"model": model, "content": []any{}, "stop_reason": nil,
				"usage": map[string]int{"input_tokens": 0, "output_tokens": 0},
			},
		})
		sseEvent(w, map[string]any{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]string{"type": "text", "text": ""},
		})
		sseEvent(w, map[string]any{"type": "ping"})

		// send response in chunks of ~20 chars so Chatbox renders progressively
		chunk := []rune(response)
		size := 20
		for i := 0; i < len(chunk); i += size {
			end := i + size
			if end > len(chunk) {
				end = len(chunk)
			}
			sseEvent(w, map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]string{"type": "text_delta", "text": string(chunk[i:end])},
			})
		}

		sseEvent(w, map[string]any{"type": "content_block_stop", "index": 0})
		sseEvent(w, map[string]any{
			"type":  "message_delta",
			"delta": map[string]string{"stop_reason": "end_turn"},
			"usage": map[string]int{"output_tokens": len(strings.Fields(response))},
		})
		sseEvent(w, map[string]any{"type": "message_stop"})
		return
	}

	// non-streaming JSON response
	jsonResp(w, http.StatusOK, anthropicResponse{
		ID:         msgID,
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    []anthropicBlock{{Type: "text", Text: response}},
		StopReason: "end_turn",
		Usage:      anthropicUsage{OutputTokens: len(strings.Fields(response))},
	})
}
