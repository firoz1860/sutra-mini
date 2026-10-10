"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import styles from "./page.module.css";
import {
  FeedbackBanner,
  Mark,
  PendingReceiptNotice,
  ReceiptForm,
  RefreshIcon,
  Skeleton,
  StatusSummary,
  VoucherSection,
  formatPaise,
  type Feedback,
  type Issue,
  type PendingOp,
  type SessionGuard,
  type Status,
  type Voucher,
} from "./ui";
import { IssuesTab, JobWorkersTab, OperatorBar, SKUsTab } from "./manage";

const PENDING_STORAGE_KEY = "sutra.pendingReceipt";
const OPERATOR_TOKEN_KEY = "sutra.operatorToken";

type Tab = "overview" | "issues" | "workers" | "skus";
const TABS: { id: Tab; label: string }[] = [
  { id: "overview", label: "Sync overview" },
  { id: "issues", label: "Job-work issues" },
  { id: "workers", label: "Job-workers" },
  { id: "skus", label: "SKUs" },
];

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
  const [refreshing, setRefreshing] = useState(false);
  const [refreshNote, setRefreshNote] = useState("");
  const [refreshFailed, setRefreshFailed] = useState(false);
  const [scrolled, setScrolled] = useState(false);
  const sentinelRef = useRef<HTMLDivElement | null>(null);
  const inFlight = useRef(false);
  const busyRef = useRef(false);

  const refreshQueued = useRef(false);

  const dataSig = useRef<string | null>(null);

  const [issueId, setIssueId] = useState("");
  const [qty, setQty] = useState("1");
  const [receivedOn, setReceivedOn] = useState(todayLocal());
  const [submitting, setSubmitting] = useState(false);
  const [pendingOp, setPendingOp] = useState<PendingOp | null>(null);
  const [discardedFp, setDiscardedFp] = useState<string | null>(null);

  const [tab, setTab] = useState<Tab>("overview");
  const [token, setToken] = useState<string | null>(null);
  const [authConfigured, setAuthConfigured] = useState<boolean | null>(null);

  const sessionGen = useRef(0);
  const pollCtl = useRef<AbortController | null>(null);


  const session = useMemo<SessionGuard>(
    () => ({ current: () => sessionGen.current, isActive: (gen: number) => sessionGen.current === gen }),
    [],
  );


  const clearSession = useCallback((expired: boolean) => {
    sessionGen.current += 1;
    pollCtl.current?.abort("session-ended");
    setToken(null);
    try {
      sessionStorage.removeItem(OPERATOR_TOKEN_KEY);
    } catch {
      /* ignore */
    }
    setVouchers(null);
    setIssues([]);
    setIssueId("");
    setLastUpdated(null);
    setLoading(true);
    setError(null);
    setIssuesError(null);
    setRetrying({});
    setTab("overview");

    setSubmitting(false);
    busyRef.current = false;
    refreshQueued.current = false;
    dataSig.current = null;
    setRefreshing(false);
    setRefreshNote("");
    setDiscardedFp(null);
    setFeedback(expired ? { kind: "err", msg: "Session expired. Please sign in again." } : null);
  }, []);

  const handleUnauthorized = useCallback(() => clearSession(true), [clearSession]);
  const logout = useCallback(() => clearSession(false), [clearSession]);

  const load = useCallback(
    async (opts?: { signal?: AbortSignal; gen?: number; manual?: boolean }) => {
      if (!token) return;
      const manual = opts?.manual ?? false;
      if (inFlight.current) {

        if (manual) refreshQueued.current = true;
        return;
      }
      inFlight.current = true;
      const gen = opts?.gen ?? sessionGen.current;
      const signal = opts?.signal;
      const headers = { Authorization: `Bearer ${token}` };
      let vouchersOk = false;
      let issuesOk = false;
      let nextVouchers: Voucher[] | null = null;
      let nextIssues: Issue[] | null = null;
      try {
        try {
          const { status, data } = await requestJSON(EP.vouchers, { headers }, 8000, signal);
          if (sessionGen.current !== gen) return;
          if (status === 401) {
            handleUnauthorized();
            return;
          }
          if (status !== 200) throw new Error(`backend returned ${status}`);
          const list = (data as { vouchers?: Voucher[] })?.vouchers;
          nextVouchers = Array.isArray(list) ? list : [];
          setVouchers(nextVouchers);
          setError(null);
          setLastUpdated(new Date());
          vouchersOk = true;
        } catch (e) {
          if (sessionGen.current !== gen) return;
          if (!(e instanceof ReqError && e.kind === "aborted")) setError(describeErr(e, "vouchers"));
        }
        try {
          const { status, data } = await requestJSON(EP.issues, { headers }, 8000, signal);
          if (sessionGen.current !== gen) return;
          if (status === 401) {
            handleUnauthorized();
            return;
          }
          if (status !== 200) throw new Error(`backend returned ${status}`);
          const list = (data as { issues?: Issue[] })?.issues;
          nextIssues = Array.isArray(list) ? list : [];
          setIssues(nextIssues);
          setIssueId((cur) => cur || (nextIssues?.[0]?.id ?? ""));
          setIssuesError(null);
          issuesOk = true;
        } catch (e) {
          if (sessionGen.current !== gen) return;
          if (!(e instanceof ReqError && e.kind === "aborted")) setIssuesError(describeErr(e, "issues"));
        }
      } finally {
        if (sessionGen.current === gen) {
          setLoading(false);
          if (vouchersOk && issuesOk) {
            const sig = JSON.stringify({ v: nextVouchers, i: nextIssues });
            if (manual) {
              const changed = dataSig.current === null || dataSig.current !== sig;
              setRefreshNote(changed ? "List updated." : "List refreshed — no changes.");
            }
            dataSig.current = sig;
          }
          if (manual) {
            setRefreshing(false);
            if (!vouchersOk && !issuesOk) {
              setRefreshFailed(true);
              setRefreshNote("Refresh failed — could not reach the server. Showing the last loaded data.");
            } else if (!vouchersOk || !issuesOk) {
              setRefreshFailed(true);
              setRefreshNote(
                `Refresh incomplete — ${vouchersOk ? "issue totals" : "vouchers"} could not be updated. Showing the last good data for that section.`,
              );
            } else {
              setRefreshFailed(false);
            }
          }
        }
        inFlight.current = false;
        if (refreshQueued.current && token && sessionGen.current === gen) {
          refreshQueued.current = false;
          void load({ gen, manual: true });
        }
      }
    },
    [token, handleUnauthorized],
  );

  useEffect(() => {
    const restored = loadPersisted();
    if (restored) setPendingOp(restored);
  }, []);

  useEffect(() => {
    if (!token) return;
    const ctl = new AbortController();
    pollCtl.current = ctl;
    const gen = sessionGen.current;
    void load({ signal: ctl.signal, gen });
    const id = setInterval(() => void load({ signal: ctl.signal, gen }), 4000);
    return () => {
      clearInterval(id);
      ctl.abort("external");
    };
  }, [load, token]);

  useEffect(() => {
    let active = true;

    const gen = session.current();
    let stored: string | null = null;
    try {
      stored = sessionStorage.getItem(OPERATOR_TOKEN_KEY);
    } catch {
      /* ignore */
    }

    if (stored) setToken(stored);
    void (async () => {
      try {
        const res = await fetch("/api/auth/session", {
          headers: stored ? { Authorization: `Bearer ${stored}` } : {},
          cache: "no-store",
        });
        const data = (await res.json().catch(() => null)) as { authenticated?: boolean; configured?: boolean } | null;
        if (!active || !session.isActive(gen)) return;
        setAuthConfigured(typeof data?.configured === "boolean" ? data.configured : null);
        if (stored && !data?.authenticated) {
          setToken(null);
          try {
            sessionStorage.removeItem(OPERATOR_TOKEN_KEY);
          } catch {
            /* ignore */
          }
        }
      } catch {
        if (active && session.isActive(gen)) setAuthConfigured(null);
      }
    })();
    return () => {
      active = false;
    };
  }, [session]);

  const login = useCallback(async (password: string): Promise<string | null> => {
    try {
      const res = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ password }),
        cache: "no-store",
      });
      const data = (await res.json().catch(() => null)) as { token?: string; error?: string } | null;
      if (res.status === 200 && data?.token) {

        sessionGen.current += 1;
        setFeedback(null);
        setRefreshNote("");
        setToken(data.token);
        setAuthConfigured(true);
        try {
          sessionStorage.setItem(OPERATOR_TOKEN_KEY, data.token);
        } catch {
          /* ignore */
        }
        return null;
      }
      if (res.status === 503) {
        setAuthConfigured(false);
        return "Operator sign-in is not configured on this deployment.";
      }
      return data?.error ?? `Sign-in failed (HTTP ${res.status}).`;
    } catch {
      return "Could not reach the server.";
    }
  }, []);

  const doSubmit = useCallback(
    async (op: PendingOp) => {
      if (!token) return;
      const gen = session.current();
      setSubmitting(true);
      setFeedback(null);
      try {
        const { status, data, parseError } = await requestJSON(
          EP.receipts,
          {
            method: "POST",
            headers: { "content-type": "application/json", "Idempotency-Key": op.key, Authorization: `Bearer ${token}` },
            body: JSON.stringify(op.body),
          },
          10000,
        );

        if (!session.isActive(gen)) return;
        if (status === 401) {

          handleUnauthorized();
          return;
        }
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
        if (!session.isActive(gen)) return;
        if (e instanceof ReqError && e.kind === "aborted") return;
        setFeedback({ kind: "err", msg: RETRY_SAFE });
      } finally {

        if (session.isActive(gen)) {
          busyRef.current = false;
          setSubmitting(false);
          void load({ gen });
        }
      }
    },
    [load, token, handleUnauthorized, session],
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
      if (!token) return;
      const gen = session.current();
      setRetrying((r) => ({ ...r, [voucherId]: true }));
      setFeedback(null);
      try {
        const { status, data } = await requestJSON(
          EP.retry(voucherId),
          { method: "POST", headers: { Authorization: `Bearer ${token}` } },
          10000,
        );
        if (!session.isActive(gen)) return;
        if (status === 401) {
          handleUnauthorized();
          return;
        }
        if (status < 200 || status >= 300) throw new Error((data as { error?: string })?.error ?? `retry failed (${status})`);
        setFeedback({ kind: "ok", msg: "Voucher queued for another attempt." });
        await load({ gen });
      } catch (e) {
        if (!session.isActive(gen)) return;
        setFeedback({ kind: "err", msg: describeErr(e, "retry") });
      } finally {
        if (session.isActive(gen)) {
          setRetrying((r) => {
            const next = { ...r };
            delete next[voucherId];
            return next;
          });
        }
      }
    },
    [load, token, handleUnauthorized, session],
  );

  const requestRefresh = useCallback(() => {
    if (!token) return;
    // Reset the live region so a repeat outcome still announces, then show the
    // refreshing state immediately — even if the click lands behind an in-flight
    // poll and is coalesced into a single queued follow-up (never dropped).
    setRefreshNote("");
    setRefreshFailed(false);
    setRefreshing(true);
    void load({ manual: true });
  }, [load, token]);

  // Cast the sticky header's shadow only once content scrolls beneath it.
  // A 1px sentinel + IntersectionObserver avoids any per-pixel scroll handler.
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    const obs = new IntersectionObserver(([entry]) => setScrolled(!entry.isIntersecting), { threshold: 0 });
    obs.observe(el);
    return () => obs.disconnect();
  }, [token]);

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
      <div ref={sentinelRef} className={styles.sentinel} aria-hidden="true" />
      <header className={`${styles.header} ${scrolled ? styles.headerScrolled : ""}`}>
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
          <OperatorBar token={token} configured={authConfigured} onLogin={login} onLogout={logout} />
          {token && (
            <>
              <span className={styles.freshness}>{freshness}</span>
              <button
                type="button"
                className={`${styles.btnSecondary} ${styles.refreshBtn}`}
                onClick={requestRefresh}
                disabled={refreshing || (loading && vouchers === null)}
                aria-busy={refreshing}
              >
                <RefreshIcon spinning={refreshing} />
                {refreshing ? "Refreshing…" : "Refresh list"}
              </button>
              <span
                className={`${styles.refreshStatus} ${refreshFailed ? styles.refreshStatusErr : ""}`}
                role="status"
                aria-live="polite"
              >
                {refreshNote}
              </span>
            </>
          )}
        </div>
      </header>

      <div className={styles.titleBlock}>
        <h1 className={styles.title}>Job-work accounting</h1>
        <p className={styles.subtitle}>Record received quantities and track their accounting sync.</p>
      </div>

      {!token ? (
        <section className={styles.card} aria-labelledby="signin-title">
          <h2 id="signin-title" className={styles.cardTitle}>
            Operator sign-in required
          </h2>
          <p className={styles.cardDesc}>
            {authConfigured === false
              ? "Operator authentication is not configured on this deployment, so operational data cannot be shown here."
              : "Sign in with the operator password (top right) to view issues and vouchers and to record receipts."}
          </p>
          <FeedbackBanner feedback={feedback} />
        </section>
      ) : (
        <>
          <nav className={styles.tabBar} aria-label="Sections">
            {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            className={`${styles.tab} ${tab === t.id ? styles.tabActive : ""}`}
            aria-current={tab === t.id ? "page" : undefined}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>

          {tab === "issues" && (
            <div className={styles.panel}>
              <IssuesTab token={token} issues={issues} session={session} onChanged={() => void load()} onUnauthorized={handleUnauthorized} />
            </div>
          )}
          {tab === "workers" && (
            <div className={styles.panel}>
              <JobWorkersTab token={token} session={session} onChanged={() => void load()} onUnauthorized={handleUnauthorized} />
            </div>
          )}
          {tab === "skus" && (
            <div className={styles.panel}>
              <SKUsTab token={token} session={session} onChanged={() => void load()} onUnauthorized={handleUnauthorized} />
            </div>
          )}

      {tab === "overview" && (
        <div className={styles.panel}>
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
        </div>
      )}
        </>
      )}

      <footer className={styles.footer}>
        <span>Counts reflect the current voucher list, not lifetime totals.</span>
        <span>Sutra Mini — reliable accounting sync</span>
      </footer>
    </main>
  );
}
