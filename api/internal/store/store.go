package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path != ":memory:" && !strings.Contains(path, "mode=memory") {
		dir := filepath.Dir(path)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("mkdir data dir: %w", err)
			}
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  slug TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  require_approval INTEGER NOT NULL DEFAULT 1,
  rollback_webhook_url TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS environments (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  name TEXT NOT NULL,
  slug TEXT NOT NULL,
  current_version TEXT NOT NULL DEFAULT 'unreleased',
  previous_version TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL,
  UNIQUE(project_id, slug)
);

CREATE TABLE IF NOT EXISTS probes (
  id TEXT PRIMARY KEY,
  environment_id TEXT NOT NULL REFERENCES environments(id),
  name TEXT NOT NULL,
  url TEXT NOT NULL,
  method TEXT NOT NULL DEFAULT 'GET',
  expected_status INTEGER NOT NULL DEFAULT 200,
  timeout_ms INTEGER NOT NULL DEFAULT 3000
);

CREATE TABLE IF NOT EXISTS promotions (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  from_env TEXT NOT NULL,
  to_env TEXT NOT NULL,
  version TEXT NOT NULL,
  previous_target_version TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  actor TEXT NOT NULL,
  approved_by TEXT NOT NULL DEFAULT '',
  rejected_by TEXT NOT NULL DEFAULT '',
  rollback_by TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  probe_report TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  webhook_status INTEGER NOT NULL DEFAULT 0,
  webhook_body TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  project_id TEXT NOT NULL,
  promotion_id TEXT NOT NULL DEFAULT '',
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  from_env TEXT NOT NULL DEFAULT '',
  to_env TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS audit_project_seq ON audit_events(project_id, seq);
CREATE INDEX IF NOT EXISTS promo_project_created ON promotions(project_id, created_at);
`)
	return err
}

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

type Project struct {
	ID                  string        `json:"id"`
	Name                string        `json:"name"`
	Slug                string        `json:"slug"`
	Description         string        `json:"description"`
	RequireApproval     bool          `json:"require_approval"`
	RollbackWebhookURL  string        `json:"rollback_webhook_url"`
	CreatedAt           string        `json:"created_at"`
	UpdatedAt           string        `json:"updated_at"`
	Environments        []Environment `json:"environments,omitempty"`
	PendingPromotions   int           `json:"pending_promotions"`
}

type Environment struct {
	ID              string  `json:"id"`
	ProjectID       string  `json:"project_id"`
	Name            string  `json:"name"`
	Slug            string  `json:"slug"`
	CurrentVersion  string  `json:"current_version"`
	PreviousVersion string  `json:"previous_version"`
	SortOrder       int     `json:"sort_order"`
	Probes          []Probe `json:"probes,omitempty"`
}

type Probe struct {
	ID             string `json:"id"`
	EnvironmentID  string `json:"environment_id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	Method         string `json:"method"`
	ExpectedStatus int    `json:"expected_status"`
	TimeoutMS      int    `json:"timeout_ms"`
}

type Promotion struct {
	ID                     string `json:"id"`
	ProjectID              string `json:"project_id"`
	FromEnv                string `json:"from_env"`
	ToEnv                  string `json:"to_env"`
	Version                string `json:"version"`
	PreviousTargetVersion  string `json:"previous_target_version"`
	Status                 string `json:"status"`
	Actor                  string `json:"actor"`
	ApprovedBy             string `json:"approved_by"`
	RejectedBy             string `json:"rejected_by"`
	RollbackBy             string `json:"rollback_by"`
	Note                   string `json:"note"`
	ProbeReport            string `json:"probe_report"`
	Error                  string `json:"error"`
	WebhookStatus          int    `json:"webhook_status"`
	WebhookBody            string `json:"webhook_body"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
}

type AuditEvent struct {
	Seq         int64  `json:"seq"`
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	PromotionID string `json:"promotion_id"`
	Actor       string `json:"actor"`
	Action      string `json:"action"`
	FromEnv     string `json:"from_env"`
	ToEnv       string `json:"to_env"`
	Version     string `json:"version"`
	Result      string `json:"result"`
	Detail      string `json:"detail"`
	CreatedAt   string `json:"created_at"`
}

type Counts struct {
	Projects                   int    `json:"projects"`
	PromotionsTotal            int    `json:"promotions_total"`
	PromotionsBlockedByHealth  int    `json:"promotions_blocked_by_health"`
	PromotionsPendingApproval  int    `json:"promotions_pending_approval"`
	PromotionsSucceeded        int    `json:"promotions_succeeded"`
	PromotionsRolledBack       int    `json:"promotions_rolled_back"`
	AuditEvents                int    `json:"audit_events"`
	Note                       string `json:"note"`
}

func (s *Store) InsertProject(p Project, envs []Environment) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO projects(id,name,slug,description,require_approval,rollback_webhook_url,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Slug, p.Description, boolInt(p.RequireApproval), p.RollbackWebhookURL, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	for _, e := range envs {
		_, err = tx.Exec(`INSERT INTO environments(id,project_id,name,slug,current_version,previous_version,sort_order)
			VALUES(?,?,?,?,?,?,?)`, e.ID, p.ID, e.Name, e.Slug, e.CurrentVersion, e.PreviousVersion, e.SortOrder)
		if err != nil {
			return err
		}
		for _, pr := range e.Probes {
			_, err = tx.Exec(`INSERT INTO probes(id,environment_id,name,url,method,expected_status,timeout_ms)
				VALUES(?,?,?,?,?,?,?)`, pr.ID, e.ID, pr.Name, pr.URL, pr.Method, pr.ExpectedStatus, pr.TimeoutMS)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) GetProject(id string) (Project, error) {
	var p Project
	var req int
	err := s.db.QueryRow(`SELECT id,name,slug,description,require_approval,rollback_webhook_url,created_at,updated_at FROM projects WHERE id=? OR slug=?`, id, id).
		Scan(&p.ID, &p.Name, &p.Slug, &p.Description, &req, &p.RollbackWebhookURL, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	p.RequireApproval = req == 1
	p.Environments, err = s.ListEnvironments(p.ID)
	if err != nil {
		return p, err
	}
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM promotions WHERE project_id=? AND status='pending_approval'`, p.ID).Scan(&p.PendingPromotions)
	return p, nil
}

func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(ids))
	for _, id := range ids {
		p, err := s.GetProject(id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) UpdateProject(id string, requireApproval *bool, webhook *string, desc *string) (Project, error) {
	p, err := s.GetProject(id)
	if err != nil {
		return p, err
	}
	if requireApproval != nil {
		p.RequireApproval = *requireApproval
	}
	if webhook != nil {
		p.RollbackWebhookURL = *webhook
	}
	if desc != nil {
		p.Description = *desc
	}
	p.UpdatedAt = Now()
	_, err = s.db.Exec(`UPDATE projects SET require_approval=?, rollback_webhook_url=?, description=?, updated_at=? WHERE id=?`,
		boolInt(p.RequireApproval), p.RollbackWebhookURL, p.Description, p.UpdatedAt, p.ID)
	if err != nil {
		return p, err
	}
	return s.GetProject(p.ID)
}

func (s *Store) ListEnvironments(projectID string) ([]Environment, error) {
	rows, err := s.db.Query(`SELECT id,project_id,name,slug,current_version,previous_version,sort_order FROM environments WHERE project_id=? ORDER BY sort_order`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var envs []Environment
	for rows.Next() {
		var e Environment
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Name, &e.Slug, &e.CurrentVersion, &e.PreviousVersion, &e.SortOrder); err != nil {
			return nil, err
		}
		envs = append(envs, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range envs {
		probes, err := s.ListProbes(envs[i].ID)
		if err != nil {
			return nil, err
		}
		envs[i].Probes = probes
	}
	return envs, nil
}

func (s *Store) GetEnvironment(id string) (Environment, error) {
	var e Environment
	err := s.db.QueryRow(`SELECT id,project_id,name,slug,current_version,previous_version,sort_order FROM environments WHERE id=?`, id).
		Scan(&e.ID, &e.ProjectID, &e.Name, &e.Slug, &e.CurrentVersion, &e.PreviousVersion, &e.SortOrder)
	if err != nil {
		return e, err
	}
	e.Probes, err = s.ListProbes(e.ID)
	return e, err
}

func (s *Store) EnvBySlug(projectID, slug string) (Environment, error) {
	var e Environment
	err := s.db.QueryRow(`SELECT id,project_id,name,slug,current_version,previous_version,sort_order FROM environments WHERE project_id=? AND slug=?`, projectID, slug).
		Scan(&e.ID, &e.ProjectID, &e.Name, &e.Slug, &e.CurrentVersion, &e.PreviousVersion, &e.SortOrder)
	if err != nil {
		return e, err
	}
	e.Probes, err = s.ListProbes(e.ID)
	return e, err
}

func (s *Store) ListProbes(envID string) ([]Probe, error) {
	rows, err := s.db.Query(`SELECT id,environment_id,name,url,method,expected_status,timeout_ms FROM probes WHERE environment_id=? ORDER BY name`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Probe
	for rows.Next() {
		var p Probe
		if err := rows.Scan(&p.ID, &p.EnvironmentID, &p.Name, &p.URL, &p.Method, &p.ExpectedStatus, &p.TimeoutMS); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []Probe{}
	}
	return out, rows.Err()
}

func (s *Store) AddProbe(p Probe) error {
	_, err := s.db.Exec(`INSERT INTO probes(id,environment_id,name,url,method,expected_status,timeout_ms) VALUES(?,?,?,?,?,?,?)`,
		p.ID, p.EnvironmentID, p.Name, p.URL, p.Method, p.ExpectedStatus, p.TimeoutMS)
	return err
}

func (s *Store) DeleteProbe(id string) error {
	res, err := s.db.Exec(`DELETE FROM probes WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) InsertPromotion(p Promotion) error {
	_, err := s.db.Exec(`INSERT INTO promotions(id,project_id,from_env,to_env,version,previous_target_version,status,actor,approved_by,rejected_by,rollback_by,note,probe_report,error,webhook_status,webhook_body,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.ProjectID, p.FromEnv, p.ToEnv, p.Version, p.PreviousTargetVersion, p.Status, p.Actor, p.ApprovedBy, p.RejectedBy, p.RollbackBy, p.Note, p.ProbeReport, p.Error, p.WebhookStatus, p.WebhookBody, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *Store) GetPromotion(id string) (Promotion, error) {
	var p Promotion
	err := s.db.QueryRow(`SELECT id,project_id,from_env,to_env,version,previous_target_version,status,actor,approved_by,rejected_by,rollback_by,note,probe_report,error,webhook_status,webhook_body,created_at,updated_at FROM promotions WHERE id=?`, id).
		Scan(&p.ID, &p.ProjectID, &p.FromEnv, &p.ToEnv, &p.Version, &p.PreviousTargetVersion, &p.Status, &p.Actor, &p.ApprovedBy, &p.RejectedBy, &p.RollbackBy, &p.Note, &p.ProbeReport, &p.Error, &p.WebhookStatus, &p.WebhookBody, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (s *Store) ListPromotions(projectID string) ([]Promotion, error) {
	rows, err := s.db.Query(`SELECT id FROM promotions WHERE project_id=? ORDER BY created_at DESC LIMIT 100`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]Promotion, 0, len(ids))
	for _, id := range ids {
		p, err := s.GetPromotion(id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) CountPending(projectID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM promotions WHERE project_id=? AND status IN ('pending_approval','checking')`, projectID).Scan(&n)
	return n, err
}

func (s *Store) FailStaleChecking(olderThan time.Duration) error {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	_, err := s.db.Exec(`UPDATE promotions SET status='failed', error='interrupted while running health checks', updated_at=? WHERE status='checking' AND created_at<?`, Now(), cutoff)
	return err
}

type ApplyOpts struct {
	Promotion Promotion
	Target    Environment
	Audit     AuditEvent
	Extra     []AuditEvent
}

// ApplyPromotion moves the version pointer on the target env, updates the
// promotion row, and inserts the audit event in one SQLite transaction.
func (s *Store) ApplyPromotion(opts ApplyOpts) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p := opts.Promotion
	t := opts.Target
	if _, err := tx.Exec(`UPDATE environments SET previous_version=current_version, current_version=? WHERE id=?`, p.Version, t.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE promotions SET status=?, approved_by=?, probe_report=?, error=?, previous_target_version=?, updated_at=? WHERE id=?`,
		p.Status, p.ApprovedBy, p.ProbeReport, p.Error, t.CurrentVersion, p.UpdatedAt, p.ID); err != nil {
		return err
	}
	for _, extra := range opts.Extra {
		if err := insertAuditTx(tx, extra); err != nil {
			return err
		}
	}
	if err := insertAuditTx(tx, opts.Audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FinishPromotion(p Promotion, a AuditEvent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE promotions SET status=?, approved_by=?, rejected_by=?, rollback_by=?, probe_report=?, error=?, webhook_status=?, webhook_body=?, updated_at=? WHERE id=?`,
		p.Status, p.ApprovedBy, p.RejectedBy, p.RollbackBy, p.ProbeReport, p.Error, p.WebhookStatus, p.WebhookBody, p.UpdatedAt, p.ID); err != nil {
		return err
	}
	if err := insertAuditTx(tx, a); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RollbackPromotion(p Promotion, target Environment, a AuditEvent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	restore := p.PreviousTargetVersion
	if restore == "" {
		restore = target.PreviousVersion
	}
	if _, err := tx.Exec(`UPDATE environments SET current_version=?, previous_version=? WHERE id=?`, restore, target.CurrentVersion, target.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE promotions SET status=?, rollback_by=?, error=?, webhook_status=?, webhook_body=?, updated_at=? WHERE id=?`,
		p.Status, p.RollbackBy, p.Error, p.WebhookStatus, p.WebhookBody, p.UpdatedAt, p.ID); err != nil {
		return err
	}
	if err := insertAuditTx(tx, a); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AppendAudit(a AuditEvent) error {
	return insertAuditTx(s.db, a)
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func insertAuditTx(e execer, a AuditEvent) error {
	if a.Detail == "" {
		a.Detail = "{}"
	}
	_, err := e.Exec(`INSERT INTO audit_events(id,project_id,promotion_id,actor,action,from_env,to_env,version,result,detail,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.ProjectID, a.PromotionID, a.Actor, a.Action, a.FromEnv, a.ToEnv, a.Version, a.Result, a.Detail, a.CreatedAt)
	return err
}

func (s *Store) ListAudit(projectID string, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if projectID == "" {
		rows, err = s.db.Query(`SELECT seq,id,project_id,promotion_id,actor,action,from_env,to_env,version,result,detail,created_at FROM audit_events ORDER BY seq DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.Query(`SELECT seq,id,project_id,promotion_id,actor,action,from_env,to_env,version,result,detail,created_at FROM audit_events WHERE project_id=? ORDER BY seq DESC LIMIT ?`, projectID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var a AuditEvent
		if err := rows.Scan(&a.Seq, &a.ID, &a.ProjectID, &a.PromotionID, &a.Actor, &a.Action, &a.FromEnv, &a.ToEnv, &a.Version, &a.Result, &a.Detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if out == nil {
		out = []AuditEvent{}
	}
	return out, rows.Err()
}

func (s *Store) AuditCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&n)
	return n, err
}

func (s *Store) Counts() (Counts, error) {
	var c Counts
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&c.Projects)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM promotions`).Scan(&c.PromotionsTotal)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM promotions WHERE status='failed' AND error LIKE '%health%'`).Scan(&c.PromotionsBlockedByHealth)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM promotions WHERE status='pending_approval'`).Scan(&c.PromotionsPendingApproval)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM promotions WHERE status='succeeded'`).Scan(&c.PromotionsSucceeded)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM promotions WHERE status='rolled_back'`).Scan(&c.PromotionsRolledBack)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&c.AuditEvents)
	c.Note = "Counts are from this local SQLite file only. They are not fleet-wide production metrics."
	return c, nil
}

func (s *Store) ProjectCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n)
	return n, err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func MustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
