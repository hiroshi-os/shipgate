package health

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"shipgate/internal/store"
)

type Result struct {
	ProbeID    string `json:"probe_id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Method     string `json:"method"`
	OK         bool   `json:"ok"`
	Status     int    `json:"status"`
	LatencyMS  int64  `json:"latency_ms"`
	Error      string `json:"error,omitempty"`
	Expected   int    `json:"expected_status"`
}

type Report struct {
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at"`
	Passed     bool     `json:"passed"`
	Results    []Result `json:"results"`
}

type Prober struct {
	Client *http.Client
}

func (p *Prober) Run(ctx context.Context, probes []store.Probe) Report {
	rep := Report{
		StartedAt: store.Now(),
		Passed:    true,
		Results:   []Result{},
	}
	if p.Client == nil {
		p.Client = &http.Client{Timeout: 10 * time.Second}
	}
	for _, pr := range probes {
		rep.Results = append(rep.Results, p.one(ctx, pr))
	}
	for _, r := range rep.Results {
		if !r.OK {
			rep.Passed = false
			break
		}
	}
	if len(probes) == 0 {
		rep.Passed = false
	}
	rep.FinishedAt = store.Now()
	return rep
}

func (p *Prober) one(ctx context.Context, pr store.Probe) Result {
	res := Result{
		ProbeID:  pr.ID,
		Name:     pr.Name,
		URL:      pr.URL,
		Method:   pr.Method,
		Expected: pr.ExpectedStatus,
	}
	if pr.Method == "" {
		pr.Method = http.MethodGet
		res.Method = http.MethodGet
	}
	timeout := time.Duration(pr.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, pr.Method, pr.URL, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("User-Agent", "shipgate-prober/1.0")
	start := time.Now()
	resp, err := p.Client.Do(req)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	res.Status = resp.StatusCode
	if resp.StatusCode != pr.ExpectedStatus {
		res.Error = fmt.Sprintf("expected HTTP %d, got %d", pr.ExpectedStatus, resp.StatusCode)
		return res
	}
	res.OK = true
	return res
}
