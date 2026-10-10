"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import styles from "./page.module.css";
import {
  FeedbackBanner,
  Mark,
  PendingReceiptNotice,
  ReceiptForm,
  Skeleton,
  StatusSummary,
  VoucherSection,
  formatPaise,
  type Feedback,
  type Issue,
  type PendingOp,
  type Status,
  type Voucher,
} from "./ui";

const PENDING_STORAGE_KEY = "sutra.pendingReceipt";

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

async function requestJSON(url: string, opts: RequestInit, timeoutMs: number, external?: AbortSignal): Promise<ReqResult> {
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
  } catch {
    if (ctl.signal.aborted) throw new ReqError(ctl.signal.reason === "timeout" ? "timeout" : "aborted");
    throw new ReqError("network");
  } finally {
    clearTimeout(timer);
    if (external) external.removeEventListener("abort", onExternalAbort);
  }
}

function describeErr(e: unknown, what: string): string {
  if (e instanceof ReqError) return e.kind === "timeout" ? `${what} request timed out` : `${what} unavailable`;
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

const RETRY_SAFE = "The request may have reached the server. Retry this receipt safely to check the result.";

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
  const [feedback, setFeedback] = useState<Feedback | null>(null);
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
            setFeedback({ kind: "err", msg: `Server returned 201 but the response was malformed or incomplete. ${RETRY_SAFE}` });
          }
        } else if (status === 409 || status === 400) {
          persist(null);
          setPendingOp(null);
          setFeedback({ kind: "err", msg: (data as { error?: string })?.error ?? `Rejected (HTTP ${status}).` });
        } else {
          setFeedback({ kind: "err", msg: `Unclear outcome (HTTP ${status}). ${RETRY_SAFE}` });
        }
      } catch (e) {
        if (e instanceof ReqError && e.kind === "aborted") return;
        setFeedback({ kind: "err", msg: RETRY_SAFE });
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
        setFeedback({ kind: "err", msg: "Choose a job-work issue first." });
        return;
      }
      if (!Number.isInteger(n) || n <= 0) {
        setFeedback({ kind: "err", msg: "Enter a whole quantity greater than zero." });
        return;
      }
      const body = { issue_id: issueId, qty: n, received_on: receivedOn };
      const fp = fingerprint(body);
      if (discardedFp && fp === discardedFp) {
        setFeedback({
          kind: "err",
          msg: "This matches a receipt you stopped tracking whose outcome was unknown — it may already exist. Check the lists before creating an identical one.",
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
      msg: "Stopped tracking this receipt here. It did not cancel a receipt that may already be recorded on the server — Retry (same key) is the safe option.",
    });
  }, [submitting, pendingOp]);

  const retryVoucher = useCallback(
    async (voucherId: string) => {
      setRetrying((r) => ({ ...r, [voucherId]: true }));
      setFeedback(null);
      try {
        const { status, data } = await requestJSON(EP.retry(voucherId), { method: "POST" }, 10000);
        if (status < 200 || status >= 300) throw new Error((data as { error?: string })?.error ?? `retry failed (${status})`);
        setFeedback({ kind: "ok", msg: "Voucher queued for another attempt." });
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
  const counts: Record<Status, number> = { pending: grouped.pending.length, pushed: grouped.pushed.length, failed: grouped.failed.length };

  const jump = (s: Status) => {
    document.getElementById(`section-${s}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const freshness = lastUpdated
    ? `List updated ${lastUpdated.toLocaleTimeString()}${error ? " (stale)" : ""}`
    : "Loading the list…";

  return (
    <main className={styles.page}>
      <header className={styles.header}>
        <div className={styles.brand}>
          <Mark />
          <span>
            <span className={styles.brandName}>Sutra Mini</span>
            <span className={styles.studio} style={{ display: "block" }}>
              Shree Radha Studio
            </span>
          </span>
        </div>
        <div className={styles.headerRight}>
          <span className={styles.freshness}>{freshness}</span>
          <button type="button" className={styles.btnSecondary} onClick={() => void load()} disabled={loading && vouchers === null}>
            Refresh list
          </button>
        </div>
      </header>

      <div className={styles.titleBlock}>
        <h1 className={styles.title}>Job-work accounting</h1>
        <p className={styles.subtitle}>Record received quantities and track their accounting sync.</p>
      </div>

      <StatusSummary counts={counts} onJump={jump} />

      <FeedbackBanner feedback={feedback} />

      {issuesError && (
        <div role="alert" className={styles.stale}>
          Could not refresh the issue list ({issuesError}). The options below may be out of date; Create receipt still
          uses the server as the source of truth.
        </div>
      )}

      <ReceiptForm
        issues={issues}
        issuesLoading={loading && issues.length === 0}
        issueId={issueId}
        qty={qty}
        receivedOn={receivedOn}
        submitting={submitting}
        locked={!!pendingOp}
        onIssue={setIssueId}
        onQty={setQty}
        onDate={setReceivedOn}
        onSubmit={createReceipt}
      />

      {pendingOp && <PendingReceiptNotice op={pendingOp} submitting={submitting} onRetry={retryPending} onDiscard={discardPending} />}

      {error && (
        <div role="alert" className={styles.stale}>
          Could not reach the API: {error}. Showing the last results; retrying automatically.
        </div>
      )}

      {vouchers === null ? (
        <Skeleton />
      ) : (
        <>
          <VoucherSection
            id="section-pending"
            title="Pending"
            description="Waiting to be sent or retried."
            emptyText="No vouchers waiting to sync."
            status="pending"
            vouchers={grouped.pending}
            retrying={retrying}
            onRetry={retryVoucher}
          />
          <VoucherSection
            id="section-pushed"
            title="Pushed"
            description="Confirmed by the accounting service."
            emptyText="Confirmed vouchers will appear here."
            status="pushed"
            vouchers={grouped.pushed}
            retrying={retrying}
            onRetry={retryVoucher}
          />
          <VoucherSection
            id="section-failed"
            title="Failed"
            description="Needs attention before another attempt."
            emptyText="No failed vouchers in the current list."
            status="failed"
            vouchers={grouped.failed}
            retrying={retrying}
            onRetry={retryVoucher}
          />
        </>
      )}

      <footer className={styles.footer}>
        <span>Counts reflect the current voucher list, not lifetime totals.</span>
        <span>Sutra Mini — reliable accounting sync</span>
      </footer>
    </main>
  );
}
