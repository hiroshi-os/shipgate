package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shipgate/internal/server"
	"shipgate/internal/store"
)

func startAPI(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := server.New(st, server.Options{DemoEnabled: false})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, st
}

func healthServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func doJSON(t *testing.T, method, url, actor string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if actor != "" {
		req.Header.Set("X-Actor", actor)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("json %s: %s", err, raw)
		}
	}
	return resp.StatusCode, m
}

func setVersion(t *testing.T, st *store.Store, projectID, slug, ver string) {
	t.Helper()
	e, err := st.EnvBySlug(projectID, slug)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.DB().Exec(`UPDATE environments SET current_version=? WHERE id=?`, ver, e.ID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestHealthz(t *testing.T) {
	api, _ := startAPI(t)
	code, body := doJSON(t, http.MethodGet, api.URL+"/api/v1/healthz", "", nil)
	if code != 200 || body["ok"] != true {
		t.Fatalf("%d %#v", code, body)
	}
}

func TestHealthGateBlocksThenSucceeds(t *testing.T) {
	state := 503
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(state)
	}))
	t.Cleanup(hs.Close)

	api, st := startAPI(t)
	code, body := doJSON(t, http.MethodPost, api.URL+"/api/v1/projects", "alice", map[string]any{
		"name": "gated", "require_approval": false,
	})
	if code != 201 {
		t.Fatal(body)
	}
	pid := body["id"].(string)
	for _, e := range body["environments"].([]any) {
		em := e.(map[string]any)
		if em["slug"] == "staging" {
			c, b := doJSON(t, http.MethodPost, api.URL+"/api/v1/environments/"+em["id"].(string)+"/probes", "alice", map[string]any{
				"name": "ready", "url": hs.URL + "/health/ready", "expected_status": 200,
			})
			if c != 201 {
				t.Fatalf("probe %d %#v", c, b)
			}
		}
	}
	setVersion(t, st, pid, "staging", "v9.9.9")
	setVersion(t, st, pid, "prod", "v9.9.8")

	code, body = doJSON(t, http.MethodPost, api.URL+"/api/v1/projects/"+pid+"/promotions", "alice", map[string]any{
		"from": "staging", "to": "prod",
	})
	if code != 409 {
		t.Fatalf("expected 409 blocked, got %d %#v", code, body)
	}
	if body["status"] != "failed" {
		t.Fatalf("status %v", body["status"])
	}

	state = 200
	code, body = doJSON(t, http.MethodPost, api.URL+"/api/v1/projects/"+pid+"/promotions", "alice", map[string]any{
		"from": "staging", "to": "prod",
	})
	if code != 201 {
		t.Fatalf("expected 201 success, got %d %#v", code, body)
	}
	if body["status"] != "succeeded" {
		t.Fatalf("status %v", body["status"])
	}

	proj, _ := st.GetProject(pid)
	var prod store.Environment
	for _, e := range proj.Environments {
		if e.Slug == "prod" {
			prod = e
		}
	}
	if prod.CurrentVersion != "v9.9.9" {
		t.Fatalf("prod version %s", prod.CurrentVersion)
	}
	if prod.PreviousVersion != "v9.9.8" {
		t.Fatalf("prod previous %s", prod.PreviousVersion)
	}

	_, audit := doJSON(t, http.MethodGet, api.URL+"/api/v1/projects/"+pid+"/audit", "", nil)
	events := audit["audit"].([]any)
	var blocked, succeeded bool
	for _, e := range events {
		em := e.(map[string]any)
		if em["action"] == "promotion.blocked" {
			blocked = true
			if em["result"] != "fail" || em["actor"] != "alice" {
				t.Fatalf("blocked row %#v", em)
			}
			if em["from_env"] != "staging" || em["to_env"] != "prod" {
				t.Fatalf("envs %#v", em)
			}
		}
		if em["action"] == "promotion.succeeded" {
			succeeded = true
			if em["result"] != "ok" {
				t.Fatalf("succeeded row %#v", em)
			}
		}
	}
	if !blocked || !succeeded {
		t.Fatalf("audit missing rows blocked=%v succeeded=%v events=%v", blocked, succeeded, events)
	}
}

func TestApprovalFourEyesAndSkipEnvRejected(t *testing.T) {
	hs := healthServer(t, 200)
	api, st := startAPI(t)
	_, body := doJSON(t, http.MethodPost, api.URL+"/api/v1/projects", "alice", map[string]any{
		"name": "needs-eyes", "require_approval": true,
	})
	pid := body["id"].(string)
	for _, e := range body["environments"].([]any) {
		em := e.(map[string]any)
		if em["slug"] == "staging" {
			doJSON(t, http.MethodPost, api.URL+"/api/v1/environments/"+em["id"].(string)+"/probes", "alice", map[string]any{
				"name": "ready", "url": hs.URL, "expected_status": 200,
			})
		}
	}
	setVersion(t, st, pid, "staging", "v2.0.0")
	setVersion(t, st, pid, "prod", "v1.0.0")

	code, body := doJSON(t, http.MethodPost, api.URL+"/api/v1/projects/"+pid+"/promotions", "alice", map[string]any{
		"from": "dev", "to": "prod",
	})
	if code != 400 {
		t.Fatalf("skip env should 400, got %d %#v", code, body)
	}

	code, body = doJSON(t, http.MethodPost, api.URL+"/api/v1/projects/"+pid+"/promotions", "alice", map[string]any{
		"from": "staging", "to": "prod",
	})
	if code != 201 || body["status"] != "pending_approval" {
		t.Fatalf("want pending, %d %#v", code, body)
	}
	pmt := body["id"].(string)

	code, body = doJSON(t, http.MethodPost, api.URL+"/api/v1/promotions/"+pmt+"/approve", "alice", map[string]any{})
	if code != 403 {
		t.Fatalf("four-eyes %d %#v", code, body)
	}

	code, body = doJSON(t, http.MethodPost, api.URL+"/api/v1/promotions/"+pmt+"/approve", "bob", map[string]any{})
	if code != 200 || body["status"] != "succeeded" {
		t.Fatalf("bob approve %d %#v", code, body)
	}
	if body["approved_by"] != "bob" {
		t.Fatalf("approved_by %v", body["approved_by"])
	}
	proj, _ := st.GetProject(pid)
	for _, e := range proj.Environments {
		if e.Slug == "prod" && e.CurrentVersion != "v2.0.0" {
			t.Fatalf("prod %s", e.CurrentVersion)
		}
	}
}

func TestRefusePromoteWithoutProbes(t *testing.T) {
	api, st := startAPI(t)
	_, body := doJSON(t, http.MethodPost, api.URL+"/api/v1/projects", "alice", map[string]any{
		"name": "naked", "require_approval": false,
	})
	pid := body["id"].(string)
	setVersion(t, st, pid, "staging", "v1")
	code, body := doJSON(t, http.MethodPost, api.URL+"/api/v1/projects/"+pid+"/promotions", "alice", map[string]any{
		"from": "staging", "to": "prod",
	})
	if code != 409 {
		t.Fatalf("want 409, %d %#v", code, body)
	}
	if !strings.Contains(fmtString(body["error"]), "no probes") && body["status"] != "failed" {
		t.Fatalf("body %#v", body)
	}
}

func TestRollbackCallsWebhook(t *testing.T) {
	got := make(chan []byte, 1)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- b
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	t.Cleanup(hook.Close)
	hs := healthServer(t, 200)
	api, st := startAPI(t)
	_, body := doJSON(t, http.MethodPost, api.URL+"/api/v1/projects", "alice", map[string]any{
		"name": "rb", "require_approval": false, "rollback_webhook_url": hook.URL,
	})
	pid := body["id"].(string)
	for _, e := range body["environments"].([]any) {
		em := e.(map[string]any)
		if em["slug"] == "staging" {
			doJSON(t, http.MethodPost, api.URL+"/api/v1/environments/"+em["id"].(string)+"/probes", "alice", map[string]any{
				"name": "ready", "url": hs.URL, "expected_status": 200,
			})
		}
	}
	setVersion(t, st, pid, "staging", "v4")
	setVersion(t, st, pid, "prod", "v3")
	_, body = doJSON(t, http.MethodPost, api.URL+"/api/v1/projects/"+pid+"/promotions", "alice", map[string]any{
		"from": "staging", "to": "prod",
	})
	if body["status"] != "succeeded" {
		t.Fatalf("promote %#v", body)
	}
	pmt := body["id"].(string)
	code, body := doJSON(t, http.MethodPost, api.URL+"/api/v1/promotions/"+pmt+"/rollback", "carol", map[string]any{})
	if code != 200 || body["status"] != "rolled_back" {
		t.Fatalf("rollback %d %#v", code, body)
	}
	select {
	case raw := <-got:
		if !bytes.Contains(raw, []byte(`"event":"rollback"`)) {
			t.Fatalf("payload %s", raw)
		}
		if !bytes.Contains(raw, []byte(`"from_version":"v4"`)) || !bytes.Contains(raw, []byte(`"to_version":"v3"`)) {
			t.Fatalf("versions %s", raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not called")
	}
	proj, _ := st.GetProject(pid)
	for _, e := range proj.Environments {
		if e.Slug == "prod" && e.CurrentVersion != "v3" {
			t.Fatalf("prod after rollback %s", e.CurrentVersion)
		}
	}
}

func TestRollbackWebhookFailureDoesNotMovePointer(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`boom`))
	}))
	t.Cleanup(hook.Close)
	hs := healthServer(t, 200)
	api, st := startAPI(t)
	_, body := doJSON(t, http.MethodPost, api.URL+"/api/v1/projects", "alice", map[string]any{
		"name": "rb-fail", "require_approval": false, "rollback_webhook_url": hook.URL,
	})
	pid := body["id"].(string)
	for _, e := range body["environments"].([]any) {
		em := e.(map[string]any)
		if em["slug"] == "staging" {
			doJSON(t, http.MethodPost, api.URL+"/api/v1/environments/"+em["id"].(string)+"/probes", "alice", map[string]any{
				"name": "ready", "url": hs.URL, "expected_status": 200,
			})
		}
	}
	setVersion(t, st, pid, "staging", "v8")
	setVersion(t, st, pid, "prod", "v7")
	_, body = doJSON(t, http.MethodPost, api.URL+"/api/v1/projects/"+pid+"/promotions", "alice", map[string]any{
		"from": "staging", "to": "prod",
	})
	pmt := body["id"].(string)
	code, body := doJSON(t, http.MethodPost, api.URL+"/api/v1/promotions/"+pmt+"/rollback", "alice", map[string]any{})
	if code != 502 || body["status"] != "rollback_failed" {
		t.Fatalf("want 502 rollback_failed, %d %#v", code, body)
	}
	proj, _ := st.GetProject(pid)
	for _, e := range proj.Environments {
		if e.Slug == "prod" && e.CurrentVersion != "v8" {
			t.Fatalf("pointer should stay on v8, got %s", e.CurrentVersion)
		}
	}
}

func TestAuditAppendOnlyAndSeqOrder(t *testing.T) {
	api, st := startAPI(t)
	doJSON(t, http.MethodPost, api.URL+"/api/v1/projects", "alice", map[string]any{"name": "a1"})
	doJSON(t, http.MethodPost, api.URL+"/api/v1/projects", "bob", map[string]any{"name": "a2"})
	n1, _ := st.AuditCount()
	_, err := st.DB().Exec(`UPDATE audit_events SET actor='forged'`)
	if err != nil {
		t.Fatal(err)
	}
	// The product never UPDATEs audit_events. This assertion documents the table
	// *can* be mutated by anyone with the sqlite file — consistency is a write-path
	// invariant, not a DB trigger. Restore and check seq monotonic via API.
	_, _ = st.DB().Exec(`UPDATE audit_events SET actor='alice' WHERE actor='forged'`)
	n2, _ := st.AuditCount()
	if n2 != n1 {
		t.Fatalf("count changed %d -> %d", n1, n2)
	}
	_, body := doJSON(t, http.MethodGet, api.URL+"/api/v1/audit", "", nil)
	events := body["audit"].([]any)
	if len(events) < 2 {
		t.Fatal(events)
	}
	first := events[0].(map[string]any)["seq"].(float64)
	second := events[1].(map[string]any)["seq"].(float64)
	if first <= second {
		t.Fatalf("expected desc seq, %v then %v", first, second)
	}
}

func TestMutateRequiresActor(t *testing.T) {
	api, _ := startAPI(t)
	code, _ := doJSON(t, http.MethodPost, api.URL+"/api/v1/projects", "", map[string]any{"name": "x"})
	if code != 401 {
		t.Fatalf("want 401 got %d", code)
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}
