package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"shipgate/internal/health"
	"shipgate/internal/ids"
	"shipgate/internal/store"
	"shipgate/internal/webhook"
)

type Options struct {
	DemoEnabled bool
	DemoAppURL  string
}

type Server struct {
	store  *store.Store
	opts   Options
	client *http.Client
	prober *health.Prober
	locks  sync.Map // projectID -> *sync.Mutex
}

func New(st *store.Store, opts Options) http.Handler {
	s := &Server{
		store: st,
		opts:  opts,
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	s.prober = &health.Prober{Client: s.client}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/healthz", s.healthz)
	mux.HandleFunc("GET /api/v1/metrics", s.metrics)
	mux.HandleFunc("GET /api/v1/projects", s.listProjects)
	mux.HandleFunc("POST /api/v1/projects", s.createProject)
	mux.HandleFunc("GET /api/v1/projects/{id}", s.getProject)
	mux.HandleFunc("PATCH /api/v1/projects/{id}", s.patchProject)
	mux.HandleFunc("POST /api/v1/environments/{id}/probes", s.addProbe)
	mux.HandleFunc("DELETE /api/v1/probes/{id}", s.deleteProbe)
	mux.HandleFunc("POST /api/v1/environments/{id}/check", s.checkEnv)
	mux.HandleFunc("GET /api/v1/projects/{id}/promotions", s.listPromotions)
	mux.HandleFunc("POST /api/v1/projects/{id}/promotions", s.createPromotion)
	mux.HandleFunc("GET /api/v1/promotions/{id}", s.getPromotion)
	mux.HandleFunc("POST /api/v1/promotions/{id}/approve", s.approve)
	mux.HandleFunc("POST /api/v1/promotions/{id}/reject", s.reject)
	mux.HandleFunc("POST /api/v1/promotions/{id}/rollback", s.rollback)
	mux.HandleFunc("GET /api/v1/projects/{id}/audit", s.projectAudit)
	mux.HandleFunc("GET /api/v1/audit", s.allAudit)
	mux.HandleFunc("GET /api/v1/demo/state", s.demoState)
	mux.HandleFunc("POST /api/v1/demo/break", s.demoBreak)
	mux.HandleFunc("POST /api/v1/demo/restore", s.demoRestore)
	return s.wrap(mux)
}

func (s *Server) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Actor")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		id := ids.New("req")
		w.Header().Set("X-Request-Id", id)
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s rid=%s", r.Method, r.URL.Path, sw.code, time.Since(start).Truncate(time.Millisecond), id)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) {
	w.code = c
	w.ResponseWriter.WriteHeader(c)
}

func (s *Server) lock(projectID string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(projectID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB().Ping(); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "db ping failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "shipgate", "time": store.Now()})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.Counts()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.store.ListProjects()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"projects": ps})
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, p)
}

type createProjectReq struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	RequireApproval    *bool  `json:"require_approval"`
	RollbackWebhookURL string `json:"rollback_webhook_url"`
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireActor(w, r)
	if !ok {
		return
	}
	var req createProjectReq
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, 400, "name is required")
		return
	}
	slug := slugify(req.Name)
	now := store.Now()
	reqApp := true
	if req.RequireApproval != nil {
		reqApp = *req.RequireApproval
	}
	p := store.Project{
		ID: ids.New("prj"), Name: req.Name, Slug: slug, Description: strings.TrimSpace(req.Description),
		RequireApproval: reqApp, RollbackWebhookURL: strings.TrimSpace(req.RollbackWebhookURL),
		CreatedAt: now, UpdatedAt: now,
	}
	envs := []store.Environment{
		{ID: ids.New("env"), ProjectID: p.ID, Name: "Development", Slug: "dev", CurrentVersion: "unreleased", SortOrder: 0},
		{ID: ids.New("env"), ProjectID: p.ID, Name: "Staging", Slug: "staging", CurrentVersion: "unreleased", SortOrder: 1},
		{ID: ids.New("env"), ProjectID: p.ID, Name: "Production", Slug: "prod", CurrentVersion: "unreleased", SortOrder: 2},
	}
	if err := s.store.InsertProject(p, envs); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, 409, "a project with that slug already exists")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	_ = s.store.AppendAudit(store.AuditEvent{
		ID: ids.New("aud"), ProjectID: p.ID, Actor: actor, Action: "project.created", Result: "ok",
		Detail: store.MustJSON(map[string]any{"name": p.Name, "require_approval": p.RequireApproval}), CreatedAt: now,
	})
	out, err := s.store.GetProject(p.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, out)
}

type patchProjectReq struct {
	RequireApproval    *bool   `json:"require_approval"`
	RollbackWebhookURL *string `json:"rollback_webhook_url"`
	Description        *string `json:"description"`
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireActor(w, r)
	if !ok {
		return
	}
	var req patchProjectReq
	if !readJSON(w, r, &req) {
		return
	}
	p, err := s.store.UpdateProject(r.PathValue("id"), req.RequireApproval, req.RollbackWebhookURL, req.Description)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	_ = s.store.AppendAudit(store.AuditEvent{
		ID: ids.New("aud"), ProjectID: p.ID, Actor: actor, Action: "project.updated", Result: "ok",
		Detail: store.MustJSON(req), CreatedAt: store.Now(),
	})
	writeJSON(w, 200, p)
}

type addProbeReq struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	Method         string `json:"method"`
	ExpectedStatus int    `json:"expected_status"`
	TimeoutMS      int    `json:"timeout_ms"`
}

func (s *Server) addProbe(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireActor(w, r)
	if !ok {
		return
	}
	env, err := s.store.GetEnvironment(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var req addProbeReq
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" || req.URL == "" {
		writeErr(w, 400, "name and url are required")
		return
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		writeErr(w, 400, "probe url must be http(s)")
		return
	}
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	if req.ExpectedStatus == 0 {
		req.ExpectedStatus = 200
	}
	if req.TimeoutMS == 0 {
		req.TimeoutMS = 3000
	}
	pr := store.Probe{
		ID: ids.New("prb"), EnvironmentID: env.ID, Name: req.Name, URL: req.URL,
		Method: strings.ToUpper(req.Method), ExpectedStatus: req.ExpectedStatus, TimeoutMS: req.TimeoutMS,
	}
	if err := s.store.AddProbe(pr); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	_ = s.store.AppendAudit(store.AuditEvent{
		ID: ids.New("aud"), ProjectID: env.ProjectID, Actor: actor, Action: "probe.added", Result: "ok",
		Detail: store.MustJSON(pr), CreatedAt: store.Now(),
	})
	writeJSON(w, 201, pr)
}

func (s *Server) deleteProbe(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireActor(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeleteProbe(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	_ = s.store.AppendAudit(store.AuditEvent{
		ID: ids.New("aud"), ProjectID: "", Actor: actor, Action: "probe.removed", Result: "ok",
		Detail: store.MustJSON(map[string]string{"probe_id": id}), CreatedAt: store.Now(),
	})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) checkEnv(w http.ResponseWriter, r *http.Request) {
	env, err := s.store.GetEnvironment(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	rep := s.prober.Run(r.Context(), env.Probes)
	writeJSON(w, 200, map[string]any{"environment": env.Slug, "report": rep})
}

func (s *Server) listPromotions(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	list, err := s.store.ListPromotions(p.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"promotions": list})
}

func (s *Server) getPromotion(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPromotion(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, p)
}

type promoReq struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Version string `json:"version"`
	Note    string `json:"note"`
}

func (s *Server) createPromotion(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireActor(w, r)
	if !ok {
		return
	}
	proj, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var req promoReq
	if !readJSON(w, r, &req) {
		return
	}
	req.From = strings.ToLower(strings.TrimSpace(req.From))
	req.To = strings.ToLower(strings.TrimSpace(req.To))

	mu := s.lock(proj.ID)
	mu.Lock()
	defer mu.Unlock()

	src, err := s.store.EnvBySlug(proj.ID, req.From)
	if err != nil {
		writeErr(w, 400, "unknown from environment")
		return
	}
	dst, err := s.store.EnvBySlug(proj.ID, req.To)
	if err != nil {
		writeErr(w, 400, "unknown to environment")
		return
	}
	if dst.SortOrder != src.SortOrder+1 {
		writeErr(w, 400, "promotions must move to the next environment (dev→staging or staging→prod); use rollback to go backwards")
		return
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = src.CurrentVersion
	}
	if version == "" || version == "unreleased" {
		writeErr(w, 400, "source environment has no version to promote")
		return
	}

	pending, err := s.store.CountPending(proj.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if pending > 0 {
		writeErr(w, 409, "a promotion is already in flight for this project")
		return
	}

	now := store.Now()
	p := store.Promotion{
		ID: ids.New("pmt"), ProjectID: proj.ID, FromEnv: src.Slug, ToEnv: dst.Slug,
		Version: version, PreviousTargetVersion: dst.CurrentVersion, Status: "checking",
		Actor: actor, Note: strings.TrimSpace(req.Note), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.InsertPromotion(p); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	_ = s.store.AppendAudit(store.AuditEvent{
		ID: ids.New("aud"), ProjectID: proj.ID, PromotionID: p.ID, Actor: actor,
		Action: "promotion.requested", FromEnv: src.Slug, ToEnv: dst.Slug, Version: version,
		Result: "pending", Detail: store.MustJSON(map[string]any{"note": p.Note}), CreatedAt: now,
	})

	p, code, err := s.runGate(r.Context(), proj, p, src, dst, actor, false)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, code, p)
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireActor(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPromotion(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if p.Status != "pending_approval" {
		writeErr(w, 409, "promotion is not waiting for approval")
		return
	}
	if actor == p.Actor {
		writeErr(w, 403, "four-eyes: the requester cannot approve their own promotion — switch actor")
		return
	}
	proj, err := s.store.GetProject(p.ProjectID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	mu := s.lock(proj.ID)
	mu.Lock()
	defer mu.Unlock()

	src, err := s.store.EnvBySlug(proj.ID, p.FromEnv)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	dst, err := s.store.EnvBySlug(proj.ID, p.ToEnv)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	p.ApprovedBy = actor
	p, code, err := s.runGate(r.Context(), proj, p, src, dst, actor, true)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, code, p)
}

func (s *Server) reject(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireActor(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPromotion(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if p.Status != "pending_approval" {
		writeErr(w, 409, "promotion is not waiting for approval")
		return
	}
	p.Status = "rejected"
	p.RejectedBy = actor
	p.UpdatedAt = store.Now()
	a := store.AuditEvent{
		ID: ids.New("aud"), ProjectID: p.ProjectID, PromotionID: p.ID, Actor: actor,
		Action: "promotion.rejected", FromEnv: p.FromEnv, ToEnv: p.ToEnv, Version: p.Version,
		Result: "fail", CreatedAt: p.UpdatedAt,
	}
	if err := s.store.FinishPromotion(p, a); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out, _ := s.store.GetPromotion(p.ID)
	writeJSON(w, 200, out)
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireActor(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPromotion(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if p.Status != "succeeded" {
		writeErr(w, 409, "only a succeeded promotion can be rolled back")
		return
	}
	proj, err := s.store.GetProject(p.ProjectID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	mu := s.lock(proj.ID)
	mu.Lock()
	defer mu.Unlock()

	dst, err := s.store.EnvBySlug(proj.ID, p.ToEnv)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if dst.CurrentVersion != p.Version {
		writeErr(w, 409, "target environment has moved since this promotion; refusing to clobber it")
		return
	}

	restore := p.PreviousTargetVersion
	payload := webhook.RollbackPayload{
		Event: "rollback", ProjectID: proj.ID, ProjectName: proj.Name, PromotionID: p.ID,
		Environment: dst.Slug, FromVersion: dst.CurrentVersion, ToVersion: restore,
		Actor: actor, At: store.Now(),
	}
	d := webhook.CallRollback(r.Context(), s.client, proj.RollbackWebhookURL, payload)
	p.WebhookStatus = d.Status
	p.WebhookBody = d.Body
	p.RollbackBy = actor
	p.UpdatedAt = store.Now()
	if d.Err != "" && proj.RollbackWebhookURL != "" {
		p.Status = "rollback_failed"
		p.Error = d.Err
		a := store.AuditEvent{
			ID: ids.New("aud"), ProjectID: proj.ID, PromotionID: p.ID, Actor: actor,
			Action: "promotion.rollback_failed", FromEnv: p.ToEnv, ToEnv: p.ToEnv, Version: restore,
			Result: "fail", Detail: store.MustJSON(map[string]any{"webhook_error": d.Err, "webhook_status": d.Status, "body": d.Body}),
			CreatedAt: p.UpdatedAt,
		}
		if err := s.store.FinishPromotion(p, a); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		out, _ := s.store.GetPromotion(p.ID)
		writeJSON(w, 502, out)
		return
	}
	p.Status = "rolled_back"
	p.Error = ""
	a := store.AuditEvent{
		ID: ids.New("aud"), ProjectID: proj.ID, PromotionID: p.ID, Actor: actor,
		Action: "promotion.rolled_back", FromEnv: p.ToEnv, ToEnv: p.ToEnv, Version: restore,
		Result: "ok", Detail: store.MustJSON(map[string]any{"from_version": dst.CurrentVersion, "to_version": restore, "webhook_status": d.Status}),
		CreatedAt: p.UpdatedAt,
	}
	if err := s.store.RollbackPromotion(p, dst, a); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out, _ := s.store.GetPromotion(p.ID)
	writeJSON(w, 200, out)
}

func (s *Server) runGate(ctx context.Context, proj store.Project, p store.Promotion, src, dst store.Environment, actor string, isApprove bool) (store.Promotion, int, error) {
	if len(src.Probes) == 0 {
		p.Status = "failed"
		p.Error = "health gate refused: source environment has no probes configured"
		p.UpdatedAt = store.Now()
		a := store.AuditEvent{
			ID: ids.New("aud"), ProjectID: proj.ID, PromotionID: p.ID, Actor: actor,
			Action: "promotion.blocked", FromEnv: src.Slug, ToEnv: dst.Slug, Version: p.Version,
			Result: "fail", Detail: store.MustJSON(map[string]string{"reason": p.Error}), CreatedAt: p.UpdatedAt,
		}
		if err := s.store.FinishPromotion(p, a); err != nil {
			return p, 500, err
		}
		out, _ := s.store.GetPromotion(p.ID)
		return out, http.StatusConflict, nil
	}

	rep := s.prober.Run(ctx, src.Probes)
	p.ProbeReport = store.MustJSON(rep)
	p.UpdatedAt = store.Now()

	if !rep.Passed {
		p.Status = "failed"
		p.Error = "health gate blocked promotion: one or more probes failed"
		a := store.AuditEvent{
			ID: ids.New("aud"), ProjectID: proj.ID, PromotionID: p.ID, Actor: actor,
			Action: "promotion.blocked", FromEnv: src.Slug, ToEnv: dst.Slug, Version: p.Version,
			Result: "fail", Detail: p.ProbeReport, CreatedAt: p.UpdatedAt,
		}
		if err := s.store.FinishPromotion(p, a); err != nil {
			return p, 500, err
		}
		out, _ := s.store.GetPromotion(p.ID)
		return out, http.StatusConflict, nil
	}

	needApproval := proj.RequireApproval && !isApprove
	if needApproval {
		p.Status = "pending_approval"
		p.Error = ""
		a := store.AuditEvent{
			ID: ids.New("aud"), ProjectID: proj.ID, PromotionID: p.ID, Actor: actor,
			Action: "promotion.queued", FromEnv: src.Slug, ToEnv: dst.Slug, Version: p.Version,
			Result: "pending", Detail: p.ProbeReport, CreatedAt: p.UpdatedAt,
		}
		if err := s.store.FinishPromotion(p, a); err != nil {
			return p, 500, err
		}
		out, _ := s.store.GetPromotion(p.ID)
		return out, http.StatusCreated, nil
	}

	p.Status = "succeeded"
	p.Error = ""
	a := store.AuditEvent{
		ID: ids.New("aud"), ProjectID: proj.ID, PromotionID: p.ID, Actor: actor,
		Action: "promotion.succeeded", FromEnv: src.Slug, ToEnv: dst.Slug, Version: p.Version,
		Result: "ok", Detail: p.ProbeReport, CreatedAt: p.UpdatedAt,
	}
	opts := store.ApplyOpts{Promotion: p, Target: dst, Audit: a}
	if isApprove {
		opts.Extra = []store.AuditEvent{{
			ID: ids.New("aud"), ProjectID: proj.ID, PromotionID: p.ID, Actor: actor,
			Action: "promotion.approved", FromEnv: src.Slug, ToEnv: dst.Slug, Version: p.Version,
			Result: "ok", Detail: p.ProbeReport, CreatedAt: p.UpdatedAt,
		}}
	}
	if err := s.store.ApplyPromotion(opts); err != nil {
		return p, 500, err
	}
	out, _ := s.store.GetPromotion(p.ID)
	code := http.StatusCreated
	if isApprove {
		code = http.StatusOK
	}
	return out, code, nil
}

func (s *Server) projectAudit(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	list, err := s.store.ListAudit(p.ID, 200)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"audit": list})
}

func (s *Server) allAudit(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAudit("", 200)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"audit": list})
}

func (s *Server) demoState(w http.ResponseWriter, r *http.Request) {
	if !s.opts.DemoEnabled {
		writeErr(w, 404, "demo controls disabled")
		return
	}
	resp, err := s.client.Get(strings.TrimRight(s.opts.DemoAppURL, "/") + "/control/state")
	if err != nil {
		writeErr(w, 502, "demo-app unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) demoBreak(w http.ResponseWriter, r *http.Request) {
	s.proxyDemo(w, r, "/control/break")
}

func (s *Server) demoRestore(w http.ResponseWriter, r *http.Request) {
	s.proxyDemo(w, r, "/control/restore")
}

func (s *Server) proxyDemo(w http.ResponseWriter, r *http.Request, path string) {
	if !s.opts.DemoEnabled {
		writeErr(w, 404, "demo controls disabled")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(s.opts.DemoAppURL, "/")+path, strings.NewReader(string(body)))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		writeErr(w, 502, "demo-app unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

var actorRe = regexp.MustCompile(`^[a-zA-Z0-9._@-]{1,64}$`)

func requireActor(w http.ResponseWriter, r *http.Request) (string, bool) {
	a := strings.TrimSpace(r.Header.Get("X-Actor"))
	if a == "" {
		writeErr(w, 401, "X-Actor header required (this MVP has no SSO; the UI operator switcher sets it)")
		return "", false
	}
	if !actorRe.MatchString(a) {
		writeErr(w, 400, "X-Actor must be 1-64 chars of [A-Za-z0-9._@-]")
		return "", false
	}
	return a, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func writeStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "not found")
		return
	}
	writeErr(w, 500, err.Error())
}

func readJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dest); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "project"
	}
	return out
}
