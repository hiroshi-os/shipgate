"use client";

import Link from "next/link";
import { FormEvent, useEffect, useState } from "react";
import { api } from "@/lib/api";
import { fmtTime, StatusPill } from "@/components/StatusPill";
import type { AuditEvent, Metrics, Project } from "@/lib/types";

export default function BoardPage() {
  const [metrics, setMetrics] = useState<Metrics | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [audit, setAudit] = useState<AuditEvent[]>([]);
  const [err, setErr] = useState("");
  const [open, setOpen] = useState(false);

  async function load() {
    try {
      const [m, p, a] = await Promise.all([api.metrics(), api.projects(), api.audit()]);
      setMetrics(m);
      setProjects(p.projects || []);
      setAudit(a.audit || []);
      setErr("");
    } catch (e) {
      setErr((e as Error).message);
    }
  }
  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, []);

  return (
    <>
      <p className="kicker">control plane</p>
      <h1 className="page-title">Nothing reaches prod until the gate opens.</h1>
      <p className="lede">
        Shipgate is a staging → production promotion airlock. HTTP health probes must pass before a
        version pointer moves. Projects can require a second operator. Every attempt is appended to
        an audit log that this process never updates.
      </p>
      {err && <div className="banner stop">{err} — is the API up on :8080?</div>}
      {metrics && (
        <div className="stats">
          <div className="stat">
            <b>{metrics.promotions_total}</b>
            <span>promotions in this database</span>
          </div>
          <div className="stat">
            <b>{metrics.promotions_blocked_by_health}</b>
            <span>blocked by health gate</span>
          </div>
          <div className="stat">
            <b>{metrics.promotions_pending_approval}</b>
            <span>waiting on four-eyes</span>
          </div>
          <div className="stat">
            <b>{metrics.promotions_succeeded}</b>
            <span>succeeded (not rolled back)</span>
          </div>
        </div>
      )}
      {metrics && <p className="muted" style={{ marginTop: -16, marginBottom: 24, fontSize: 12 }}>{metrics.note}</p>}

      <div className="row-head">
        <h2>Projects</h2>
        <button className="btn sm" onClick={() => setOpen((v) => !v)}>
          {open ? "Close" : "New project"}
        </button>
      </div>
      {open && <NewProject onDone={() => { setOpen(false); load(); }} />}
      <div className="cards">
        {projects.map((p) => (
          <Link key={p.id} href={`/projects/${p.id}`} className="card">
            <div className="card-top">
              <div>
                <h3>{p.name}</h3>
                <p>{p.description || "—"}</p>
              </div>
              <div className="pills">
                {p.require_approval ? <span className="pill hold">approval on</span> : <span className="pill ok">auto if healthy</span>}
                {p.pending_promotions > 0 && <span className="pill hold">{p.pending_promotions} pending</span>}
              </div>
            </div>
            <div className="pipeline">
              {(p.environments || []).map((e, i) => (
                <span key={e.id} style={{ display: "contents" }}>
                  {i > 0 && <div className="gate">→</div>}
                  <div className={`chamber ${e.slug}`}>
                    <div className="env-name">{e.slug}</div>
                    <div className="ver">{e.current_version}</div>
                    <div className="probe-n">{e.probes?.length || 0} probe{e.probes?.length === 1 ? "" : "s"}</div>
                  </div>
                </span>
              ))}
            </div>
          </Link>
        ))}
        {projects.length === 0 && !err && <div className="card muted">No projects yet.</div>}
      </div>

      <div className="row-head" style={{ marginTop: 32 }}>
        <h2>Flight recorder</h2>
        <Link href="/audit" className="muted">
          Full log →
        </Link>
      </div>
      <div className="card" style={{ overflowX: "auto" }}>
        <table className="table">
          <thead>
            <tr>
              <th>seq</th>
              <th>when (local)</th>
              <th>actor</th>
              <th>action</th>
              <th>from → to</th>
              <th>result</th>
            </tr>
          </thead>
          <tbody>
            {audit.slice(0, 12).map((a) => (
              <tr key={a.id}>
                <td className="mono">{a.seq}</td>
                <td className="mono">{fmtTime(a.created_at)}</td>
                <td className="mono">{a.actor}</td>
                <td>{a.action}</td>
                <td className="mono">
                  {a.from_env || "—"} {a.to_env ? `→ ${a.to_env}` : ""}
                </td>
                <td>
                  <StatusPill status={a.result} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function NewProject({ onDone }: { onDone: () => void }) {
  const [err, setErr] = useState("");
  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    try {
      await api.createProject({
        name: String(fd.get("name") || ""),
        description: String(fd.get("description") || ""),
        require_approval: fd.get("require_approval") === "on",
        rollback_webhook_url: String(fd.get("rollback_webhook_url") || ""),
      });
      onDone();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  }
  return (
    <form className="card" onSubmit={onSubmit} style={{ marginBottom: 14 }}>
      <div className="form-grid">
        <label>
          Name
          <input name="name" required placeholder="payments-api" />
        </label>
        <label>
          Rollback webhook URL
          <input name="rollback_webhook_url" placeholder="http://hooks:8090/hooks/rollback" />
        </label>
        <label className="span2">
          Description
          <input name="description" placeholder="What this service does" />
        </label>
        <label>
          <span>
            <input type="checkbox" name="require_approval" defaultChecked /> Require four-eyes approval
          </span>
        </label>
      </div>
      {err && <p className="err">{err}</p>}
      <div className="form-actions">
        <button className="btn" type="submit">
          Create project
        </button>
      </div>
    </form>
  );
}
