"use client";

import { FormEvent, useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { api, getActor } from "@/lib/api";
import { fmtTime, StatusPill } from "@/components/StatusPill";
import type { AuditEvent, DemoState, Environment, ProbeReport, Project, Promotion } from "@/lib/types";

export default function ProjectPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const [proj, setProj] = useState<Project | null>(null);
  const [promos, setPromos] = useState<Promotion[]>([]);
  const [audit, setAudit] = useState<AuditEvent[]>([]);
  const [demo, setDemo] = useState<DemoState | null>(null);
  const [err, setErr] = useState("");
  const [flash, setFlash] = useState<{ kind: string; text: string } | null>(null);
  const [report, setReport] = useState<ProbeReport | null>(null);
  const [busy, setBusy] = useState("");

  async function load() {
    try {
      const [p, pr, a] = await Promise.all([api.project(id), api.promotions(id), api.audit(id)]);
      setProj(p);
      setPromos(pr.promotions || []);
      setAudit(a.audit || []);
      setErr("");
    } catch (e) {
      setErr((e as Error).message);
    }
    try {
      setDemo(await api.demoState());
    } catch {
      setDemo(null);
    }
  }
  useEffect(() => {
    load();
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [id]);

  if (err && !proj) return <div className="banner stop">{err}</div>;
  if (!proj) return <p className="muted">Loading…</p>;

  const bySlug = Object.fromEntries((proj.environments || []).map((e) => [e.slug, e]));

  async function promote(from: string, to: string) {
    setBusy("promote");
    setFlash(null);
    const res = await api.promote(proj!.id, { from, to });
    setBusy("");
    const data = res.data;
    if (data.probe_report) {
      try {
        setReport(JSON.parse(data.probe_report));
      } catch {
        /* ignore */
      }
    }
    if (data.status === "failed") {
      setFlash({ kind: "stop", text: data.error || "Health gate blocked this promotion." });
    } else if (data.status === "pending_approval") {
      setFlash({ kind: "hold", text: `Queued for four-eyes. Switch operator (you are ${getActor()}) and approve.` });
    } else if (data.status === "succeeded") {
      setFlash({ kind: "ok", text: `Promoted ${data.version} ${from} → ${to}.` });
    } else if (!res.ok) {
      setFlash({ kind: "stop", text: data.error || "Promote failed." });
    }
    await load();
  }

  async function onApprove(pid: string) {
    setBusy("approve");
    const res = await api.approve(pid);
    setBusy("");
    const data = res.data as Promotion & { error?: string };
    if (data.probe_report) {
      try {
        setReport(JSON.parse(data.probe_report));
      } catch {
        /* ignore */
      }
    }
    if (!res.ok) {
      setFlash({ kind: "stop", text: data.error || "Approve failed (four-eyes: requester cannot approve)." });
    } else {
      setFlash({ kind: "ok", text: `Approved. ${data.version} is now on ${data.to_env}.` });
    }
    await load();
  }

  return (
    <>
      <p className="kicker">{proj.slug}</p>
      <div className="row-head">
        <h1 className="page-title">{proj.name}</h1>
        <div className="pills">
          {proj.require_approval ? <span className="pill hold">approval on</span> : <span className="pill ok">auto if healthy</span>}
        </div>
      </div>
      <p className="lede">{proj.description}</p>
      {flash && <div className={`banner ${flash.kind}`}>{flash.text}</div>}

      <div className="pipeline" style={{ marginBottom: 18 }}>
        {["dev", "staging", "prod"].map((slug, i) => {
          const e = bySlug[slug];
          if (!e) return null;
          return (
            <span key={slug} style={{ display: "contents" }}>
              {i > 0 && (
                <div className="gate" style={{ minWidth: 72 }}>
                  <button
                    className="btn sm"
                    disabled={busy !== ""}
                    onClick={() => promote(["dev", "staging"][i - 1], slug)}
                    title={`Promote ${["dev", "staging"][i - 1]} → ${slug}`}
                  >
                    Promote
                  </button>
                  <span style={{ fontSize: 11, marginTop: 6 }}>gate</span>
                </div>
              )}
              <Chamber env={e} onCheck={async () => {
                setBusy("check");
                try {
                  const r = await api.checkEnv(e.id);
                  setReport(r.report);
                } catch (ex) {
                  setFlash({ kind: "stop", text: (ex as Error).message });
                }
                setBusy("");
              }} />
            </span>
          );
        })}
      </div>

      {report && <ProbePanel report={report} />}

      <div className="lab">
        <div className="card">
          <div className="row-head">
            <h2>Promotions</h2>
          </div>
          {promos.length === 0 && <p className="muted">None yet. Use a Promote gate on the pipeline.</p>}
          {promos.map((p) => (
            <div key={p.id} style={{ padding: "12px 0", borderBottom: "1px solid var(--line)" }}>
              <div style={{ display: "flex", justifyContent: "space-between", gap: 8, flexWrap: "wrap" }}>
                <div>
                  <div className="mono">
                    {p.version} · {p.from_env} → {p.to_env}
                  </div>
                  <div className="muted" style={{ fontSize: 12 }}>
                    {p.actor} · {fmtTime(p.created_at)} {p.approved_by ? `· approved ${p.approved_by}` : ""}
                  </div>
                  {p.error && <div className="stop" style={{ fontSize: 12, marginTop: 4 }}>{p.error}</div>}
                </div>
                <div className="actions">
                  <StatusPill status={p.status} />
                  {p.status === "pending_approval" && (
                    <>
                      <button className="btn sm" disabled={busy !== ""} onClick={() => onApprove(p.id)}>
                        Approve
                      </button>
                      <button className="btn sm ghost" disabled={busy !== ""} onClick={async () => { await api.reject(p.id); load(); }}>
                        Reject
                      </button>
                    </>
                  )}
                  {p.status === "succeeded" && (
                    <button
                      className="btn sm warn"
                      disabled={busy !== ""}
                      onClick={async () => {
                        setBusy("rb");
                        const res = await api.rollback(p.id);
                        setBusy("");
                        const data = res.data as Promotion & { error?: string };
                        setFlash(
                          res.ok
                            ? { kind: "ok", text: `Rolled back. Webhook HTTP ${data.webhook_status || 0}.` }
                            : { kind: "stop", text: data.error || "Rollback failed; version pointer not moved." },
                        );
                        load();
                      }}
                    >
                      Rollback
                    </button>
                  )}
                </div>
              </div>
            </div>
          ))}
        </div>

        <div>
          <DemoLab demo={demo} onChange={async (kind) => {
            try {
              if (kind === "break") setDemo(await api.demoBreak());
              else setDemo(await api.demoRestore());
              setFlash({
                kind: kind === "break" ? "stop" : "ok",
                text: kind === "break"
                  ? "Staging probes will now fail (ready + payments → 503). Promote should 409."
                  : "Probes restored to 200. Promote can pass the health gate.",
              });
            } catch (e) {
              setFlash({ kind: "stop", text: (e as Error).message });
            }
          }} />
          <Settings proj={proj} onSaved={load} />
        </div>
      </div>

      <AddProbe envs={proj.environments} onAdded={load} />

      <div className="row-head" style={{ marginTop: 28 }}>
        <h2>Audit for this project</h2>
      </div>
      <div className="card" style={{ overflowX: "auto" }}>
        <table className="table">
          <thead>
            <tr>
              <th>seq</th>
              <th>when</th>
              <th>actor</th>
              <th>action</th>
              <th>path</th>
              <th>version</th>
              <th>result</th>
            </tr>
          </thead>
          <tbody>
            {audit.map((a) => (
              <tr key={a.id}>
                <td className="mono">{a.seq}</td>
                <td className="mono">{fmtTime(a.created_at)}</td>
                <td className="mono">{a.actor}</td>
                <td>{a.action}</td>
                <td className="mono">{a.from_env ? `${a.from_env} → ${a.to_env}` : "—"}</td>
                <td className="mono">{a.version || "—"}</td>
                <td><StatusPill status={a.result} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function Chamber({ env, onCheck }: { env: Environment; onCheck: () => void }) {
  return (
    <div className={`chamber ${env.slug}`}>
      <div className="env-name">{env.name}</div>
      <div className="ver">{env.current_version}</div>
      {env.previous_version && <div className="probe-n">was {env.previous_version}</div>}
      <div className="probe-n">{env.probes.length} probe{env.probes.length === 1 ? "" : "s"}</div>
      <button className="btn sm ghost" style={{ marginTop: 10 }} onClick={onCheck}>
        Check probes
      </button>
      {env.probes.map((p) => (
        <div key={p.id} className="probe-n" title={p.url}>
          {p.name} · expect {p.expected_status}
        </div>
      ))}
    </div>
  );
}

function ProbePanel({ report }: { report: ProbeReport }) {
  return (
    <div className={`banner ${report.passed ? "ok" : "stop"}`} style={{ marginBottom: 18 }}>
      <strong>{report.passed ? "Gate open" : "Gate closed"}</strong>
      <span className="muted"> · {report.started_at}</span>
      {report.results.map((r) => (
        <div key={r.probe_id} className="probe-row">
          <span>
            {r.name} <span className="mono muted">{r.url}</span>
          </span>
          <span className={r.ok ? "ok" : "stop"}>
            {r.ok ? "pass" : "fail"} · HTTP {r.status || "—"} · {r.latency_ms}ms {r.error ? `· ${r.error}` : ""}
          </span>
        </div>
      ))}
    </div>
  );
}

function DemoLab({ demo, onChange }: { demo: DemoState | null; onChange: (k: "break" | "restore") => void }) {
  return (
    <div className="card" style={{ marginBottom: 14 }}>
      <div className="row-head">
        <h2>Demo lab</h2>
      </div>
      {!demo && <p className="muted">Demo controls are off (API started without DEMO_SEED/DEMO_CONTROLS).</p>}
      {demo && (
        <>
          <p className="muted" style={{ marginTop: 0 }}>
            This flips the sample app behind staging probes. It is not a production feature.
          </p>
          <div className="pills" style={{ marginBottom: 10 }}>
            <span className={`pill ${demo.live ? "ok" : "stop"}`}>live {demo.live ? "200" : "503"}</span>
            <span className={`pill ${demo.ready ? "ok" : "stop"}`}>ready {demo.ready ? "200" : "503"}</span>
            <span className={`pill ${demo.payments ? "ok" : "stop"}`}>payments {demo.payments ? "200" : "503"}</span>
          </div>
          <div className="actions">
            <button className="btn sm danger" onClick={() => onChange("break")}>
              Break staging probes
            </button>
            <button className="btn sm" onClick={() => onChange("restore")}>
              Restore probes
            </button>
          </div>
        </>
      )}
    </div>
  );
}

function Settings({ proj, onSaved }: { proj: Project; onSaved: () => void }) {
  const [err, setErr] = useState("");
  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    try {
      await api.patchProject(proj.id, {
        require_approval: fd.get("require_approval") === "on",
        rollback_webhook_url: String(fd.get("rollback_webhook_url") || ""),
        description: String(fd.get("description") || ""),
      });
      onSaved();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  }
  return (
    <form className="card" onSubmit={onSubmit}>
      <div className="row-head">
        <h2>Project settings</h2>
      </div>
      <label>
        Description
        <input name="description" defaultValue={proj.description} />
      </label>
      <label style={{ marginTop: 10 }}>
        Rollback webhook
        <input name="rollback_webhook_url" defaultValue={proj.rollback_webhook_url} />
      </label>
      <label style={{ marginTop: 10 }}>
        <span>
          <input type="checkbox" name="require_approval" defaultChecked={proj.require_approval} /> Four-eyes approval
        </span>
      </label>
      {err && <p className="err">{err}</p>}
      <div className="form-actions">
        <button className="btn sm ghost" type="submit">
          Save
        </button>
      </div>
    </form>
  );
}

function AddProbe({ envs, onAdded }: { envs: Environment[]; onAdded: () => void }) {
  const [err, setErr] = useState("");
  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fd = new FormData(e.currentTarget);
    try {
      await api.addProbe(String(fd.get("env")), {
        name: String(fd.get("name")),
        url: String(fd.get("url")),
        expected_status: Number(fd.get("expected_status") || 200),
      });
      (e.target as HTMLFormElement).reset();
      onAdded();
    } catch (ex) {
      setErr((ex as Error).message);
    }
  }
  return (
    <form className="card" onSubmit={onSubmit} style={{ marginTop: 14 }}>
      <div className="row-head">
        <h2>Add HTTP probe</h2>
      </div>
      <div className="form-grid">
        <label>
          Environment
          <select name="env">
            {envs.map((e) => (
              <option key={e.id} value={e.id}>
                {e.slug}
              </option>
            ))}
          </select>
        </label>
        <label>
          Name
          <input name="name" required placeholder="ready" />
        </label>
        <label className="span2">
          URL
          <input name="url" required placeholder="http://demo-app:8080/health/ready" />
        </label>
        <label>
          Expected status
          <input name="expected_status" type="number" defaultValue={200} />
        </label>
      </div>
      {err && <p className="err">{err}</p>}
      <div className="form-actions">
        <button className="btn sm ghost" type="submit">
          Add probe
        </button>
      </div>
    </form>
  );
}
