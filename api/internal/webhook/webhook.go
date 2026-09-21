package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type RollbackPayload struct {
	Event        string `json:"event"`
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	PromotionID  string `json:"promotion_id"`
	Environment  string `json:"environment"`
	FromVersion  string `json:"from_version"`
	ToVersion    string `json:"to_version"`
	Actor        string `json:"actor"`
	At           string `json:"at"`
}

type Delivery struct {
	Status int
	Body   string
	Err    string
}

func CallRollback(ctx context.Context, client *http.Client, url string, payload RollbackPayload) Delivery {
	if url == "" {
		return Delivery{Status: 0, Body: "no webhook configured; shipgate will only move the version pointer"}
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return Delivery{Err: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "shipgate-rollback/1.0")
	req.Header.Set("X-Shipgate-Event", "rollback")
	resp, err := client.Do(req)
	if err != nil {
		return Delivery{Err: err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	d := Delivery{Status: resp.StatusCode, Body: string(b)}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		d.Err = fmt.Sprintf("webhook HTTP %d", resp.StatusCode)
	}
	return d
}
