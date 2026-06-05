package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

type jobStatus string

const (
	statusPending   jobStatus = "pending"
	statusCompleted jobStatus = "completed"
)

type job struct {
	ID       string    `json:"id"`
	Message  string    `json:"message"`
	Status   jobStatus `json:"status"`
	Response string    `json:"response,omitempty"`
	Error    string    `json:"error,omitempty"`
	created  time.Time
}

var (
	mu      sync.RWMutex
	jobsMap = make(map[string]*job)
	pending = make(chan string, 256)
)

func init() {
	// clean up jobs older than 10 minutes
	go func() {
		for range time.Tick(2 * time.Minute) {
			mu.Lock()
			for id, j := range jobsMap {
				if time.Since(j.created) > 10*time.Minute {
					delete(jobsMap, id)
				}
			}
			mu.Unlock()
		}
	}()
}

func newID() string {
	return fmt.Sprintf("%x%x", time.Now().UnixNano(), rand.Uint32())
}

// POST /send  — client posts a message, gets back {"id":"..."} immediately
func sendHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		jsonResp(w, http.StatusBadRequest, map[string]string{"error": `body must be {"message":"..."}`})
		return
	}

	id := newID()
	j := &job{ID: id, Message: req.Message, Status: statusPending, created: time.Now()}

	mu.Lock()
	jobsMap[id] = j
	mu.Unlock()

	select {
	case pending <- id:
	default:
		mu.Lock()
		delete(jobsMap, id)
		mu.Unlock()
		jsonResp(w, http.StatusServiceUnavailable, map[string]string{"error": "queue full"})
		return
	}

	jsonResp(w, http.StatusAccepted, map[string]string{"id": id})
}

// GET /result/{id}  — client polls until status != "pending"
func resultHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mu.RLock()
	j, ok := jobsMap[id]
	mu.RUnlock()

	if !ok {
		jsonResp(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	jsonResp(w, http.StatusOK, j)
}

// GET /poll  — local bridge calls this to receive the next pending job (waits up to 8s)
func pollHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case id := <-pending:
		mu.RLock()
		j, ok := jobsMap[id]
		mu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		jsonResp(w, http.StatusOK, j)
	case <-time.After(8 * time.Second):
		w.WriteHeader(http.StatusNoContent)
	}
}

// POST /done/{id}  — local bridge posts the result back
func doneHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var result struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		jsonResp(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	mu.Lock()
	j, ok := jobsMap[id]
	if ok {
		j.Status = statusCompleted
		j.Response = result.Response
		j.Error = result.Error
	}
	mu.Unlock()

	if !ok {
		jsonResp(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func jsonResp(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
