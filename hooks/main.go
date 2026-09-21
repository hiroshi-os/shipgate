package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type Event struct {
	ReceivedAt string          `json:"received_at"`
	Headers    map[string]string `json:"headers"`
	Body       json.RawMessage `json:"body"`
}

type Sink struct {
	mu     sync.Mutex
	events []Event
}

func main() {
	s := &Sink{events: []Event{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hooks/rollback", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		ev := Event{
			ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Headers: map[string]string{
				"X-Shipgate-Event": r.Header.Get("X-Shipgate-Event"),
				"User-Agent":       r.Header.Get("User-Agent"),
			},
			Body: raw,
		}
		s.mu.Lock()
		s.events = append([]Event{ev}, s.events...)
		if len(s.events) > 50 {
			s.events = s.events[:50]
		}
		s.mu.Unlock()
		log.Printf("rollback hook: %s", raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	})
	mux.HandleFunc("GET /hooks", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"events": s.events})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`ok`))
	})
	addr := getenv("LISTEN_ADDR", ":8090")
	log.Printf("hooks sink on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
