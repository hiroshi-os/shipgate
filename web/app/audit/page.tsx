"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { fmtTime, StatusPill } from "@/components/StatusPill";
import type { AuditEvent } from "@/lib/types";

export default function AuditPage() {
  const [rows, setRows] = useState<AuditEvent[]>([]);
  const [err, setErr] = useState("");
  useEffect(() => {
    let alive = true;
    async function load() {
      try {
        const a = await api.audit();
        if (alive) {
          setRows(a.audit || []);
          setErr("");
        }
      } catch (e) {
        if (alive) setErr((e as Error).message);
      }
    }
    load();
    const t = setInterval(load, 4000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);
  return (
    <>
      <p className="kicker">append-only</p>
      <h1 className="page-title">Flight recorder</h1>
      <p className="lede">
        Every mutation that matters gets an audit row with a monotonic <span className="mono">seq</span>, a
        server UTC timestamp, actor, action, from→to, version, and result. The API never UPDATE/DELETEs
        these rows. Seq is the total order.
      </p>
      {err && <div className="banner stop">{err}</div>}
      <div className="card" style={{ overflowX: "auto" }}>
        <table className="table">
          <thead>
            <tr>
              <th>seq</th>
              <th>when (local)</th>
              <th>actor</th>
              <th>action</th>
              <th>project</th>
              <th>from → to</th>
              <th>version</th>
              <th>result</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((a) => (
              <tr key={a.id}>
                <td className="mono">{a.seq}</td>
                <td className="mono" title={a.created_at}>{fmtTime(a.created_at)}</td>
                <td className="mono">{a.actor}</td>
                <td>{a.action}</td>
                <td className="mono">{a.project_id}</td>
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
