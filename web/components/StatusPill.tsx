export function StatusPill({ status }: { status: string }) {
  const cls =
    status === "succeeded" || status === "ok" || status === "rolled_back"
      ? "ok"
      : status === "pending_approval" || status === "checking" || status === "pending"
        ? "hold"
        : status === "failed" || status === "rejected" || status === "rollback_failed" || status === "fail"
          ? "stop"
          : "";
  return <span className={`pill ${cls}`}>{status.replaceAll("_", " ")}</span>;
}

export function fmtTime(iso: string) {
  try {
    const d = new Date(iso);
    return d.toLocaleString(undefined, { hour12: false });
  } catch {
    return iso;
  }
}
