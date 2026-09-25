import type { AuditEvent, DemoState, Metrics, Project, Promotion } from "./types";

const ACTOR_KEY = "shipgate.actor";

export const OPERATORS = ["alice", "bob", "carol", "ci-bot"] as const;

export function getActor(): string {
  if (typeof window === "undefined") return "alice";
  return localStorage.getItem(ACTOR_KEY) || "alice";
}

export function setActor(name: string) {
  localStorage.setItem(ACTOR_KEY, name);
  window.dispatchEvent(new Event("shipgate-actor"));
}

async function req<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("X-Actor", getActor());
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(path, { ...init, headers });
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      throw new Error(text);
    }
  }
  if (!res.ok) {
    const err = (data as { error?: string } | null)?.error;
    const wrapped = new Error(err || `HTTP ${res.status}`) as Error & { status: number; body: unknown };
    wrapped.status = res.status;
    wrapped.body = data;
    throw wrapped;
  }
  return data as T;
}

export const api = {
  metrics: () => req<Metrics>("/api/v1/metrics"),
  projects: () => req<{ projects: Project[] }>("/api/v1/projects"),
  project: (id: string) => req<Project>(`/api/v1/projects/${id}`),
  createProject: (body: { name: string; description: string; require_approval: boolean; rollback_webhook_url?: string }) =>
    req<Project>("/api/v1/projects", { method: "POST", body: JSON.stringify(body) }),
  patchProject: (id: string, body: Record<string, unknown>) =>
    req<Project>(`/api/v1/projects/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  addProbe: (envId: string, body: { name: string; url: string; expected_status?: number; timeout_ms?: number }) =>
    req(`/api/v1/environments/${envId}/probes`, { method: "POST", body: JSON.stringify(body) }),
  checkEnv: (envId: string) =>
    req<{ environment: string; report: import("./types").ProbeReport }>(`/api/v1/environments/${envId}/check`, { method: "POST", body: "{}" }),
  promotions: (id: string) => req<{ promotions: Promotion[] }>(`/api/v1/projects/${id}/promotions`),
  promote: (id: string, body: { from: string; to: string; version?: string; note?: string }) =>
    fetch("/api/v1/projects/" + id + "/promotions", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Actor": getActor() },
      body: JSON.stringify(body),
    }).then(async (res) => {
      const data = await res.json();
      return { ok: res.ok, status: res.status, data: data as Promotion & { error?: string } };
    }),
  approve: (id: string) =>
    fetch("/api/v1/promotions/" + id + "/approve", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Actor": getActor() },
      body: "{}",
    }).then(async (res) => ({ ok: res.ok, status: res.status, data: await res.json() })),
  reject: (id: string) => req<Promotion>(`/api/v1/promotions/${id}/reject`, { method: "POST", body: "{}" }),
  rollback: (id: string) =>
    fetch("/api/v1/promotions/" + id + "/rollback", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Actor": getActor() },
      body: "{}",
    }).then(async (res) => ({ ok: res.ok, status: res.status, data: await res.json() })),
  audit: (projectId?: string) =>
    req<{ audit: AuditEvent[] }>(projectId ? `/api/v1/projects/${projectId}/audit` : "/api/v1/audit"),
  demoState: () => req<DemoState>("/api/v1/demo/state"),
  demoBreak: () => req<DemoState>("/api/v1/demo/break", { method: "POST", body: JSON.stringify({ target: "all" }) }),
  demoRestore: () => req<DemoState>("/api/v1/demo/restore", { method: "POST", body: "{}" }),
};
