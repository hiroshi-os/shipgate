package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"shipgate/internal/seed"
	"shipgate/internal/server"
	"shipgate/internal/store"
)

func main() {
	path := env("DATABASE_PATH", "data/shipgate.db")
	st, err := store.Open(path)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()
	if err := st.FailStaleChecking(2 * time.Minute); err != nil {
		log.Printf("stale checking recovery: %v", err)
	}

	opts := server.Options{
		DemoEnabled: env("DEMO_SEED", "") == "true" || env("DEMO_CONTROLS", "") == "true",
		DemoAppURL:  env("DEMO_APP_URL", "http://demo-app:8080"),
	}
	if env("DEMO_SEED", "") == "true" {
		if err := seed.Run(st, opts.DemoAppURL, env("ROLLBACK_SINK_URL", "")); err != nil {
			log.Fatalf("seed: %v", err)
		}
	}

	addr := env("LISTEN_ADDR", ":8080")
	log.Printf("shipgate api on %s db=%s demo=%v", addr, path, opts.DemoEnabled)
	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(st, opts),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
