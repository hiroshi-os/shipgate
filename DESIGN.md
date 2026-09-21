# Shipgate design

Staging → production **promotion airlock**. Not a CI system, not an orchestrator, not an IDE.
Shipgate is the control plane that answers: *may this version move from staging to prod, who said so, and did we write that down?*

## What it is

A single-tenant Go API + Next.js console. SQLite is the source of truth. A docker-compose stack
ships a sample HTTP app whose `/health/*` endpoints can be failed and restored live, plus a
rollback webhook sink.

```
 operator UI (:3000) ──rewrites /api/v1──► shipgate API (:8080)
                                                │
                         HTTP GET probes        ├── SQLite
                         (staging URLs)         │     projects / envs / probes
                                                │     promotions
                         POST rollback JSON     │     audit_events (append-only)
                                                │
                                        demo-app (:8088)     hooks sink (:8090)
```

## Domain

| noun | meaning |
| --- | --- |
| Project | A service you ship. Toggle `require_approval`. Optional `rollback_webhook_url`. |
| Environment | `dev` → `staging` → `prod` (fixed order). Holds a **version pointer**, not the bits. |
| Probe | HTTP GET (MVP) against a URL, expected status, timeout. Attached to an environment. |
| Promotion | Intent to copy the source env version onto the next env. |
| Audit event | Immutable row: who / action / from→to / version / result / time / seq. |

Promotions may only move **one step forward**. Backward is **rollback** of a succeeded promotion,
which calls the project's webhook then restores the previous version pointer.

## Gate

On `POST /projects/{id}/promotions` and again on `POST /promotions/{id}/approve`:

1. Source env must have **at least one probe**. Zero probes is a hard fail (not a vacuous pass).
2. Probes run **sequentially**. Each has its own timeout. Redirects are not followed.
3. Any non-expected status or transport error → promotion `failed`, HTTP **409**, audit `promotion.blocked`.
4. If the project `require_approval` is on and this is the request (not the approve): status
   `pending_approval`. Version pointer does **not** move.
5. Approver **must differ** from requester (four-eyes). Approver's click re-runs probes.
6. On success, one SQLite transaction: move version pointer, mark promotion `succeeded`, insert
   `promotion.succeeded` audit row.

## Audit log consistency

This is the part that has to be true under failure, not just in the happy path.

- `audit_events.seq` is `INTEGER PRIMARY KEY AUTOINCREMENT`. It is the **total order**.
- Rows are **INSERT only** in application code. There is no UPDATE/DELETE of audit events.
- Timestamps are **server UTC** (`RFC3339Nano`). The UI may format locally; the stored value is UTC.
- Every version-pointer change is **the same transaction** as its audit insert:
  - `ApplyPromotion` — env update + promotion update + `promotion.succeeded`
  - `RollbackPromotion` — env restore + promotion update + `promotion.rolled_back`
  - `FinishPromotion` — terminal status (`failed`, `pending_approval`, `rejected`, `rollback_failed`) + matching audit row
- If the process dies during probes, a row may remain `checking`. On startup, rows older than two
  minutes in `checking` are marked `failed` with error `interrupted while running health checks`.
  That recovery itself is a promotion update, not an audit rewrite of the original request row
  (the original `promotion.requested` row stays).
- **Honest limit:** anyone with the SQLite file can `UPDATE` audit rows. Consistency is a
  write-path invariant of this process, not a cryptographic ledger. A production port would
  use Postgres with a role that has INSERT-only on `audit_events`, plus hashed payload chaining
  if you need tamper evidence.

## Failure modes

| failure | what the operator sees | what the database does |
| --- | --- | --- |
| Probe HTTP ≠ expected | 409, `status=failed`, per-probe latency + status in `probe_report` | promotion + `promotion.blocked` in one tx |
| Probe timeout / DNS | same, `error` is the transport error | same |
| No probes on source | 409, refused | `promotion.blocked` |
| Skip env (dev→prod) | 400, nothing created | nothing |
| Second in-flight promo | 409 | nothing new |
| Requester approves self | 403 | pending row unchanged |
| Staging goes unhealthy between request and approve | 409 on approve, `failed` | `promotion.blocked`; prod pointer unchanged |
| Rollback webhook 5xx / timeout | 502, `rollback_failed` | **prod pointer not moved**; audit `promotion.rollback_failed` |
| Rollback webhook unset | 200, `rolled_back` | pointer restored; audit notes no hook |
| Target env moved since promote | 409, refuse clobber | no change |
| Concurrent promotes | per-project mutex + sqlite `MaxOpenConns=1` | serialized |

## Rollback hook contract

`POST {rollback_webhook_url}`

```json
{
  "event": "rollback",
  "project_id": "prj_checkout",
  "project_name": "checkout-api",
  "promotion_id": "pmt_…",
  "environment": "prod",
  "from_version": "v1.4.2",
  "to_version": "v1.4.1",
  "actor": "alice",
  "at": "2026-09-21T13:00:00Z"
}
```

Headers: `Content-Type: application/json`, `X-Shipgate-Event: rollback`, `User-Agent: shipgate-rollback/1.0`.

Expect **2xx**. The hook should be **idempotent**. Shipgate does not run shell scripts — document a
tiny adapter (`curl` the URL from a wrapper, or have the receiver exec your script). If the hook
fails, Shipgate will **not** pretend prod was rolled back.

## Identity (honest)

There is no SSO in this MVP. Mutating requests require `X-Actor: [A-Za-z0-9._@-]{1,64}`. The UI
operator switcher sets it. Treat this as a demo stand-in for the IdP subject, not authentication.

## Probes and SSRF

Probe URLs are operator-configured. The API will call them. This MVP is single-tenant on a trusted
compose network. A multi-tenant port needs an allowlist (CIDR / hostname) before this is internet-facing.

## Metrics

`GET /api/v1/metrics` counts **this SQLite file**. The payload includes a `note` saying so. Do not
present those numbers as fleet SLOs.

## Why SQLite

One binary, one volume, WAL, tests that don't need Docker. Promotion apply is a short transaction
on one box. When you outgrow it: Postgres, same schema, same tx boundaries.

## Out of scope (deliberate)

Build pipelines, artifact stores, k8s rollouts, feature flags, LLM release notes, chatbots, RAG.
Shipgate gates a **version pointer** and records the decision.
