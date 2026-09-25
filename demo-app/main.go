package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
)

type State struct {
	mu       sync.Mutex
	Ready    bool   `json:"ready"`
	Payments bool   `json:"payments"`
	Live     bool   `json:"live"`
	Version  string `json:"version"`
}

func main() {
	s := &State{Ready: true, Payments: true, Live: true, Version: getenv("VERSION", "v1.4.2")}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ok := s.Live
		s.mu.Unlock()
		writeHealth(w, "live", ok)
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ok := s.Ready
		s.mu.Unlock()
		writeHealth(w, "ready", ok)
	})
	mux.HandleFunc("GET /health/payments", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ok := s.Payments
		s.mu.Unlock()
		writeHealth(w, "payments", ok)
	})
	mux.HandleFunc("GET /control/state", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, s)
	})
	mux.HandleFunc("POST /control/break", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Target string `json:"target"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		defer s.mu.Unlock()
		switch req.Target {
		case "payments":
			s.Payments = false
		case "live":
			s.Live = false
		default:
			s.Ready = false
			s.Payments = false
		}
		log.Printf("demo-app: probes broken target=%q ready=%v payments=%v", req.Target, s.Ready, s.Payments)
		writeJSON(w, 200, s)
	})
	mux.HandleFunc("POST /control/restore", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.Ready, s.Payments, s.Live = true, true, true
		s.mu.Unlock()
		log.Printf("demo-app: probes restored")
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, s)
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ready, pay, live, ver := s.Ready, s.Payments, s.Live, s.Version
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page(ready, pay, live, ver)))
	})
	addr := getenv("LISTEN_ADDR", ":8080")
	log.Printf("demo-app %s version=%s", addr, s.Version)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeHealth(w http.ResponseWriter, name string, ok bool) {
	if !ok {
		writeJSON(w, 503, map[string]any{"status": "fail", "check": name})
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "check": name})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func page(ready, pay, live bool, ver string) string {
	badge := func(ok bool) string {
		if ok {
			return `<span class="ok">200</span>`
		}
		return `<span class="bad">503</span>`
	}
	return `<!doctype html>
<html><head><meta charset="utf-8"><title>demo-app</title>
<style>
  body{font-family:ui-monospace,monospace;background:#07080a;color:#eef1f4;margin:0;padding:32px}
  h1{font-family:Georgia,serif;font-weight:600;letter-spacing:-.02em}
  .ok{color:#3ee0a2} .bad{color:#ff4d6a}
  code{background:#161a22;padding:2px 6px;border-radius:4px}
  p{color:#8b93a0;max-width:42rem;line-height:1.5}
</style></head>
<body>
<h1>demo-app</h1>
<p>Stand-in for a staging service. Shipgate probes these endpoints before a promote is allowed to succeed.</p>
<p>version <code>` + ver + `</code></p>
<ul>
<li>/health/live ` + badge(live) + `</li>
<li>/health/ready ` + badge(ready) + `</li>
<li>/health/payments ` + badge(pay) + `</li>
</ul>
<p>POST <code>/control/break</code> and <code>/control/restore</code>, or use the Demo lab in the Shipgate UI.</p>
</body></html>`
}
