export type Probe = {
  id: string;
  environment_id: string;
  name: string;
  url: string;
  method: string;
  expected_status: number;
  timeout_ms: number;
};

export type Environment = {
  id: string;
  project_id: string;
  name: string;
  slug: string;
  current_version: string;
  previous_version: string;
  sort_order: number;
  probes: Probe[];
};

export type Project = {
  id: string;
  name: string;
  slug: string;
  description: string;
  require_approval: boolean;
  rollback_webhook_url: string;
  created_at: string;
  updated_at: string;
  environments: Environment[];
  pending_promotions: number;
};

export type Promotion = {
  id: string;
  project_id: string;
  from_env: string;
  to_env: string;
  version: string;
  previous_target_version: string;
  status: string;
  actor: string;
  approved_by: string;
  rejected_by: string;
  rollback_by: string;
  note: string;
  probe_report: string;
  error: string;
  webhook_status: number;
  webhook_body: string;
  created_at: string;
  updated_at: string;
};

export type AuditEvent = {
  seq: number;
  id: string;
  project_id: string;
  promotion_id: string;
  actor: string;
  action: string;
  from_env: string;
  to_env: string;
  version: string;
  result: string;
  detail: string;
  created_at: string;
};

export type ProbeResult = {
  probe_id: string;
  name: string;
  url: string;
  method: string;
  ok: boolean;
  status: number;
  latency_ms: number;
  error?: string;
  expected_status: number;
};

export type ProbeReport = {
  started_at: string;
  finished_at: string;
  passed: boolean;
  results: ProbeResult[];
};

export type Metrics = {
  projects: number;
  promotions_total: number;
  promotions_blocked_by_health: number;
  promotions_pending_approval: number;
  promotions_succeeded: number;
  promotions_rolled_back: number;
  audit_events: number;
  note: string;
};

export type DemoState = {
  ready: boolean;
  payments: boolean;
  live: boolean;
  version: string;
};
