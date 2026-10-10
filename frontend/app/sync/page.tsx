"use client";

import { useCallback, useEffect, useRef, useState } from "react";

type Status = "pending" | "pushed" | "failed";

type Voucher = {
  id: string;
  type: string;
  source_id: string;
  amount_paise: number;
  status: Status;
  attempts: number;
  last_error: string | null;
  client_ref: string;
  external_ref: string | null;
  next_attempt_at: string;
  created_at: string;
  updated_at: string;
};

type Issue = {
  id: string;
  issued_qty: number;
  received_qty: number;
  outstanding_qty: number;
  sku: { code: string; name: string };
  party: { name: string };
};

type PendingOp = { key: string; body: { issue_id: string; qty: number; received_on: string } };

const PENDING_STORAGE_KEY = "sutra.pendingReceipt";

const inr = new Intl.NumberFormat("en-IN", { style: "currency", currency: "INR" });
const formatPaise = (p: number) => inr.format(p / 100);
const formatTime = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
};

function todayLocal(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

function fingerprint(body: PendingOp["body"]): string {
  return `${body.issue_id}|${body.qty}|${body.received_on}`;
}

class ReqError extends Error {
  kind: "timeout" | "aborted" | "network";
  constructor(kind: "timeout" | "aborted" | "network") {
    super(kind);
    this.kind = kind;
  }
}

type ReqResult = { status: number; data: unknown; parseError: boolean };

async function requestJSON(
  url: string,
  opts: RequestInit,
  timeoutMs: number,
  external?: AbortSignal,
): Promise<ReqResult> {
  const ctl = new AbortController();
  const onExternalAbort = () => ctl.abort("external");
  if (external) {
    if (external.aborted) ctl.abort("external");
    else external.addEventListener("abort", onExternalAbort, { once: true });
  }
  const timer = setTimeout(() => ctl.abort("timeout"), timeoutMs);
  try {
    const res = await fetch(url, { ...opts, cache: "no-store", signal: ctl.signal });
    const text = await res.text();
    let data: unknown;
    let parseError = false;
    if (text.trim().length > 0) {
      try {
        data = JSON.parse(text);
      } catch {
        parseError = true;
      }
    }
    return { status: res.status, data, parseError };
  } catch (e) {
    if (ctl.signal.aborted) {
      throw new ReqError(ctl.signal.reason === "timeout" ? "timeout" : "aborted");
    }
    throw new ReqError("network");
  } finally {
    clearTimeout(timer);
    if (external) external.removeEventListener("abort", onExternalAbort);
  }
}

function describeErr(e: unknown, what: string): string {
  if (e instanceof ReqError) {
    if (e.kind === "timeout") return `${what} request timed out`;
    return `${what} unavailable`;
  }
  if (e instanceof Error) return e.message;
  return `failed to load ${what}`;
}

function isValidReceiptResponse(data: unknown): data is {
  receipt: { id: string };
  voucher: { id: string; amount_paise: number };
  issue: { issued_qty: number; received_qty: number; outstanding_qty: number };
} {
  if (!data || typeof data !== "object") return false;
  const d = data as Record<string, unknown>;
  const r = d.receipt as Record<string, unknown> | undefined;
  const v = d.voucher as Record<string, unknown> | undefined;
  const i = d.issue as Record<string, unknown> | undefined;
  return (
    !!r && typeof r.id === "string" && r.id.length > 0 &&
    !!v && typeof v.id === "string" && v.id.length > 0 && typeof v.amount_paise === "number" &&
    !!i && typeof i.issued_qty === "number" && typeof i.received_qty === "number" && typeof i.outstanding_qty === "number"
  );
}

function loadPersisted(): PendingOp | null {
  try {
    const raw = sessionStorage.getItem(PENDING_STORAGE_KEY);
    if (!raw) return null;
    const op = JSON.parse(raw) as PendingOp;
    if (op && typeof op.key === "string" && op.body && typeof op.body.issue_id === "string") return op;
  } catch {
    /* ignore */
  }
  return null;
}

function persist(op: PendingOp | null) {
  try {
    if (op) sessionStorage.setItem(PENDING_STORAGE_KEY, JSON.stringify(op));
    else sessionStorage.removeItem(PENDING_STORAGE_KEY);
  } catch {
    /* ignore */
  }
}

const GROUPS: { key: Status; title: string; color: string }[] = [
  { key: "pending", title: "Pending", color: "#b45309" },
  { key: "pushed", title: "Pushed", color: "#15803d" },
  { key: "failed", title: "Failed", color: "#b91c1c" },
];

const EP = {
  vouchers: "/api/vouchers",
  issues: "/api/issues",
  receipts: "/api/receipts",
  retry: (id: string) => `/api/vouchers/${id}/retry`,
};

export default function SyncPage() {
  const [vouchers, setVouchers] = useState<Voucher[] | null>(null);
  const [issues, setIssues] = useState<Issue[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [issuesError, setIssuesError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [retrying, setRetrying] = useState<Record<string, boolean>>({});
  const [feedback, setFeedback] = useState<{ kind: "ok" | "err"; msg: string } | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const inFlight = useRef(false);
  const busyRef = useRef(false);

  const [issueId, setIssueId] = useState("");
  const [qty, setQty] = useState("1");
  const [receivedOn, setReceivedOn] = useState(todayLocal());
  const [submitting, setSubmitting] = useState(false);
  const [pendingOp, setPendingOp] = useState<PendingOp | null>(null);
  const [discardedFp, setDiscardedFp] = useState<string | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    if (inFlight.current) return;
    inFlight.current = true;
    try {
      try {
        const { status, data } = await requestJSON(EP.vouchers, {}, 8000, signal);
        if (status !== 200) throw new Error(`backend returned ${status}`);
        const list = (data as { vouchers?: Voucher[] })?.vouchers;
        setVouchers(Array.isArray(list) ? list : []);
        setError(null);
        setLastUpdated(new Date());
      } catch (e) {
        if (!(e instanceof ReqError && e.kind === "aborted")) setError(describeErr(e, "vouchers"));
      }
      try {
        const { status, data } = await requestJSON(EP.issues, {}, 8000, signal);
        if (status !== 200) throw new Error(`backend returned ${status}`);
        const list = (data as { issues?: Issue[] })?.issues;
        const issuesList = Array.isArray(list) ? list : [];
        setIssues(issuesList);
        setIssueId((cur) => cur || (issuesList[0]?.id ?? ""));
        setIssuesError(null);
      } catch (e) {
        if (!(e instanceof ReqError && e.kind === "aborted")) setIssuesError(describeErr(e, "issues"));
      }
    } finally {
      setLoading(false);
      inFlight.current = false;
    }
  }, []);

  useEffect(() => {
    const restored = loadPersisted();
    if (restored) setPendingOp(restored);
  }, []);

  useEffect(() => {
    const ctl = new AbortController();
    void load(ctl.signal);
    const id = setInterval(() => void load(ctl.signal), 4000);
    return () => {
      clearInterval(id);
      ctl.abort("external");
    };
  }, [load]);

  const doSubmit = useCallback(
    async (op: PendingOp) => {
      setSubmitting(true);
      setFeedback(null);
      try {
        const { status, data, parseError } = await requestJSON(
          EP.receipts,
          {
            method: "POST",
            headers: { "content-type": "application/json", "Idempotency-Key": op.key },
            body: JSON.stringify(op.body),
          },
          10000,
        );
        if (status === 201) {
          if (!parseError && isValidReceiptResponse(data)) {
            persist(null);
            setPendingOp(null);
            setFeedback({
              kind: "ok",
              msg: `Receipt created — voucher ${formatPaise(data.voucher.amount_paise)} queued (pending). Outstanding now ${data.issue.outstanding_qty}.`,
            });
          } else {
            setFeedback({
              kind: "err",
              msg: `Server returned 201 but the response was malformed or incomplete — the receipt may exist. Use "Retry" to resubmit safely with the same key.`,
            });
          }
        } else if (status === 409 || status === 400) {
          persist(null);
          setPendingOp(null);
          const msg = (data as { error?: string })?.error ?? `Rejected (HTTP ${status}).`;
          setFeedback({ kind: "err", msg });
        } else {
          setFeedback({
            kind: "err",
            msg: `Unclear outcome (HTTP ${status}). Use "Retry" to resubmit safely with the same key.`,
          });
        }
      } catch (e) {
        if (e instanceof ReqError && e.kind === "aborted") return;
        const timedOut = e instanceof ReqError && e.kind === "timeout";
        setFeedback({
          kind: "err",
          msg: `${timedOut ? "Request timed out" : "Network error"} — the receipt may or may not have been created. Use "Retry" to resubmit safely with the same key.`,
        });
      } finally {
        busyRef.current = false;
        setSubmitting(false);
        void load();
      }
    },
    [load],
  );

  const createReceipt = useCallback(
    (e: React.FormEvent) => {
      e.preventDefault();
      if (busyRef.current || submitting || pendingOp) return;
      const n = Number(qty);
      if (!issueId) {
        setFeedback({ kind: "err", msg: "Pick an issue first." });
        return;
      }
      if (!Number.isInteger(n) || n <= 0) {
        setFeedback({ kind: "err", msg: "Quantity must be a positive whole number." });
        return;
      }
      const body = { issue_id: issueId, qty: n, received_on: receivedOn };
      const fp = fingerprint(body);
      if (discardedFp && fp === discardedFp) {
        setFeedback({
          kind: "err",
          msg: `This is identical to a submit you just discarded whose outcome was unknown — it may already exist. Change the quantity or date, or verify the lists, before creating an identical one.`,
        });
        return;
      }
      setDiscardedFp(null);
      const op: PendingOp = { key: crypto.randomUUID(), body };
      busyRef.current = true;
      persist(op);
      setPendingOp(op);
      void doSubmit(op);
    },
    [submitting, pendingOp, qty, issueId, receivedOn, discardedFp, doSubmit],
  );

  const retryPending = useCallback(() => {
    if (busyRef.current || !pendingOp || submitting) return;
    busyRef.current = true;
    void doSubmit(pendingOp);
  }, [pendingOp, submitting, doSubmit]);

  const discardPending = useCallback(() => {
    if (submitting || !pendingOp) return;
    setDiscardedFp(fingerprint(pendingOp.body));
    persist(null);
    setPendingOp(null);
    setFeedback({
      kind: "err",
      msg: `Stopped tracking this submit here. This did NOT cancel a receipt that may already be recorded on the server — "Retry" (same key) is the safe option. Check the lists before creating an identical one.`,
    });
  }, [submitting, pendingOp]);

  const retry = useCallback(
    async (voucherId: string) => {
      setRetrying((r) => ({ ...r, [voucherId]: true }));
      setFeedback(null);
      try {
        const { status, data } = await requestJSON(EP.retry(voucherId), { method: "POST" }, 10000);
        if (status < 200 || status >= 300) {
          throw new Error((data as { error?: string })?.error ?? `retry failed (${status})`);
        }
        setFeedback({ kind: "ok", msg: "Voucher re-queued for sync." });
        await load();
      } catch (e) {
        setFeedback({ kind: "err", msg: describeErr(e, "retry") });
      } finally {
        setRetrying((r) => {
          const next = { ...r };
          delete next[voucherId];
          return next;
        });
      }
    },
    [load],
  );

  const grouped: Record<Status, Voucher[]> = { pending: [], pushed: [], failed: [] };
  for (const v of vouchers ?? []) if (grouped[v.status]) grouped[v.status].push(v);

  return (
    <main style={{ maxWidth: 980, margin: "0 auto", padding: "24px 16px" }}>
      <header style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", flexWrap: "wrap", gap: 12 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: 24 }}>Sutra Mini — Voucher Sync</h1>
          <p style={{ margin: "4px 0 0", color: "#555", fontSize: 14 }}>
            {lastUpdated ? `Last updated ${lastUpdated.toLocaleTimeString()}` : "Loading…"}
          </p>
        </div>
        <button onClick={() => void load()} style={btn} disabled={loading && vouchers === null}>
          Refresh
        </button>
      </header>

      {feedback && (
        <div role="status" style={banner(feedback.kind === "ok")}>
          {feedback.msg}
        </div>
      )}
      {error && (
        <div role="alert" style={banner(false)}>
          Could not reach the API: {error}. Showing last known data; retrying automatically…
        </div>
      )}

      <section style={{ ...card, marginTop: 20 }}>
        <h2 style={{ fontSize: 16, margin: "0 0 12px" }}>Create a receipt</h2>
        {issuesError && (
          <div role="alert" style={{ ...banner(false), marginTop: 0, marginBottom: 12 }}>
            Could not refresh issues ({issuesError}); the list below may be stale.
          </div>
        )}
        <form onSubmit={createReceipt} style={{ display: "flex", flexWrap: "wrap", gap: 12, alignItems: "flex-end" }}>
          <label style={fieldCol}>
            <span style={lbl}>Job-work issue</span>
            <select value={issueId} onChange={(e) => setIssueId(e.target.value)} style={{ ...input, minWidth: 320 }} disabled={!!pendingOp}>
              {issues.length === 0 && <option value="">No issues found</option>}
              {issues.map((i) => (
                <option key={i.id} value={i.id} disabled={i.outstanding_qty <= 0}>
                  {i.sku.code} · {i.party.name} — outstanding {i.outstanding_qty} of {i.issued_qty}
                </option>
              ))}
            </select>
          </label>
          <label style={fieldCol}>
            <span style={lbl}>Quantity</span>
            <input type="number" min={1} value={qty} onChange={(e) => setQty(e.target.value)} style={{ ...input, width: 90 }} disabled={!!pendingOp} />
          </label>
          <label style={fieldCol}>
            <span style={lbl}>Received on</span>
            <input type="date" value={receivedOn} onChange={(e) => setReceivedOn(e.target.value)} style={input} disabled={!!pendingOp} />
          </label>
          <button type="submit" style={btn} disabled={submitting || !!pendingOp || issues.length === 0}>
            {submitting ? "Creating…" : "Create receipt"}
          </button>
        </form>

        {pendingOp && (
          <div style={{ ...banner(false), marginBottom: 0 }}>
            An unresolved submit is saved (key {pendingOp.key.slice(0, 8)}…). Its outcome is unknown and it is kept
            across reloads. Resubmitting reuses the <strong>same idempotency key and payload</strong>, so it can never
            create a duplicate receipt.
            <div style={{ marginTop: 8, display: "flex", gap: 8 }}>
              <button onClick={retryPending} style={btn} disabled={submitting}>
                {submitting ? "Retrying…" : "Retry"}
              </button>
              <button onClick={discardPending} style={{ ...btn, background: "#6b7280" }} disabled={submitting}>
                Discard
              </button>
            </div>
          </div>
        )}

        <p style={{ margin: "10px 0 0", fontSize: 12, color: "#777" }}>
          Each new submit gets one idempotency key for the whole operation, saved before dispatch; retries reuse that
          key and payload, so a lost response never creates a second receipt. Receiving more than the outstanding
          quantity is rejected.
        </p>
      </section>

      {loading && vouchers === null && <p style={{ color: "#555" }}>Loading…</p>}

      {vouchers !== null &&
        GROUPS.map((g) => (
          <section key={g.key} style={{ marginTop: 24 }}>
            <h2 style={{ fontSize: 16, color: g.color, borderBottom: `2px solid ${g.color}`, paddingBottom: 6 }}>
              {g.title} ({grouped[g.key].length})
            </h2>
            {grouped[g.key].length === 0 ? (
              <p style={{ color: "#777", fontSize: 14 }}>No {g.title.toLowerCase()} vouchers.</p>
            ) : (
              <ul style={{ listStyle: "none", padding: 0, margin: 0 }}>
                {grouped[g.key].map((v) => (
                  <li key={v.id} style={{ ...card, marginTop: 10 }}>
                    <div style={{ display: "flex", justifyContent: "space-between", gap: 12, flexWrap: "wrap" }}>
                      <div>
                        <div style={{ fontWeight: 600 }}>{formatPaise(v.amount_paise)}</div>
                        <div style={{ fontSize: 12, color: "#555" }}>{v.type} · source {v.source_id.slice(0, 8)}…</div>
                        <div style={{ fontSize: 12, color: "#555" }}>client_ref: {v.client_ref}</div>
                      </div>
                      <div style={{ textAlign: "right", fontSize: 12, color: "#555" }}>
                        <div>attempts: {v.attempts}</div>
                        <div>updated: {formatTime(v.updated_at)}</div>
                        {v.external_ref && <div>external_ref: {v.external_ref}</div>}
                      </div>
                    </div>
                    {v.last_error && (
                      <div style={{ marginTop: 8, fontSize: 12, color: "#b91c1c", wordBreak: "break-word" }}>
                        last error: {v.last_error}
                      </div>
                    )}
                    {v.status === "failed" && (
                      <div style={{ marginTop: 10 }}>
                        <button onClick={() => retry(v.id)} disabled={!!retrying[v.id]} style={btn}>
                          {retrying[v.id] ? "Retrying…" : "Retry"}
                        </button>
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </section>
        ))}
    </main>
  );
}

const btn: React.CSSProperties = {
  background: "#1d4ed8",
  color: "#fff",
  border: "none",
  borderRadius: 6,
  padding: "8px 16px",
  fontSize: 14,
  cursor: "pointer",
};
const card: React.CSSProperties = {
  background: "#fff",
  border: "1px solid #e5e7eb",
  borderRadius: 8,
  padding: 14,
};
const input: React.CSSProperties = {
  padding: "7px 10px",
  fontSize: 14,
  border: "1px solid #cbd5e1",
  borderRadius: 6,
};
const fieldCol: React.CSSProperties = { display: "flex", flexDirection: "column", gap: 4 };
const lbl: React.CSSProperties = { fontSize: 12, color: "#555" };
const banner = (ok: boolean): React.CSSProperties => ({
  margin: "16px 0",
  padding: "10px 14px",
  borderRadius: 6,
  background: ok ? "#ecfdf5" : "#fef2f2",
  color: ok ? "#065f46" : "#991b1b",
  border: `1px solid ${ok ? "#a7f3d0" : "#fecaca"}`,
});
