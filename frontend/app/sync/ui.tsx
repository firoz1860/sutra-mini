"use client";

import type { ReactNode } from "react";
import styles from "./page.module.css";

export type Status = "pending" | "pushed" | "failed";

export type Voucher = {
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

export type Issue = {
  id: string;
  issued_qty: number;
  received_qty: number;
  outstanding_qty: number;
  issued_on?: string;
  unit_rate_paise?: number;
  sku: { id?: string; code: string; name: string; image_url?: string };
  party: { id?: string; name: string; phone?: string };
};

export type PendingOp = { key: string; body: { issue_id: string; qty: number; received_on: string } };
export type Feedback = { kind: "ok" | "err"; msg: string };


export type SessionGuard = {
  current: () => number;
  isActive: (gen: number) => boolean;
};

const inr = new Intl.NumberFormat("en-IN", { style: "currency", currency: "INR" });
export const formatPaise = (p: number) => inr.format(p / 100);
export const formatTime = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
};

const STATUS_STYLE: Record<Status, string> = {
  pending: styles.statusPending,
  pushed: styles.statusPushed,
  failed: styles.statusFailed,
};
const STATUS_WORD: Record<Status, string> = { pending: "Pending", pushed: "Pushed", failed: "Failed" };

export function Mark() {
  return (
    <svg className={styles.mark} viewBox="0 0 32 32" fill="none" role="img" aria-label="Sutra Mini">
      <rect x="1" y="1" width="30" height="30" rx="8" stroke="currentColor" strokeWidth="1.6" />
      <path
        d="M21 11c-1.6-1.5-4-1.9-6-1-2.3 1-3 3.6-1.3 5.2 1.5 1.4 4.6 1.3 6.1 2.8 1.7 1.6 1 4.2-1.3 5.2-2 .9-4.4.5-6-1"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function FeedbackBanner({ feedback }: { feedback: Feedback | null }) {
  if (!feedback) return null;
  return (
    <div role="status" className={`${styles.feedback} ${feedback.kind === "ok" ? styles.feedbackOk : styles.feedbackErr}`}>
      {feedback.msg}
    </div>
  );
}

export function RefreshIcon({ spinning }: { spinning?: boolean }) {
  return (
    <svg
      className={`${styles.refreshIcon} ${spinning ? styles.refreshIconSpin : ""}`}
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden="true"
    >
      <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
      <path d="M13.6 2.4v2.8h-2.8" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function StatusSummary({ counts, onJump }: { counts: Record<Status, number>; onJump: (s: Status) => void }) {
  const order: Status[] = ["pending", "pushed", "failed"];
  return (
    <div className={styles.summary}>
      {order.map((s) => (
        <button
          key={s}
          type="button"
          className={styles.summaryCard}
          onClick={() => onJump(s)}
          aria-label={`${STATUS_WORD[s]}: ${counts[s]} in the current list. Go to section.`}
        >
          <span className={`${styles.summaryIcon} ${STATUS_STYLE[s]}`} aria-hidden="true">
            <StatusGlyph status={s} />
          </span>
          <span>
            <span className={`${styles.summaryCount} ${styles.num}`}>{counts[s]}</span>
            <span className={styles.summaryLabel}>{STATUS_WORD[s]} in the current list</span>
          </span>
        </button>
      ))}
    </div>
  );
}

function StatusGlyph({ status }: { status: Status }) {
  if (status === "pushed") {
    return (
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path d="M3 8.5l3 3 7-7" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  }
  if (status === "failed") {
    return (
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path d="M8 4v5M8 11.5v.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    );
  }
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <circle cx="8" cy="8" r="5.5" stroke="currentColor" strokeWidth="1.6" />
      <path d="M8 5v3l2 1.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  );
}

export function ReceiptForm(props: {
  issues: Issue[];
  issuesLoading: boolean;
  issueId: string;
  qty: string;
  receivedOn: string;
  submitting: boolean;
  locked: boolean;
  onIssue: (v: string) => void;
  onQty: (v: string) => void;
  onDate: (v: string) => void;
  onSubmit: (e: React.FormEvent) => void;
}) {
  const { issues, issuesLoading, issueId, qty, receivedOn, submitting, locked, onIssue, onQty, onDate, onSubmit } = props;
  const selected = issues.find((i) => i.id === issueId);
  return (
    <section className={styles.card} aria-labelledby="form-title">
      <h2 id="form-title" className={styles.cardTitle}>
        Record a receipt
      </h2>
      <p className={styles.cardDesc}>Choose the job-work issue and enter the quantity received.</p>
      <form onSubmit={onSubmit} className={styles.form}>
        <div className={styles.field}>
          <label htmlFor="issue" className={styles.label}>
            Job-work issue
          </label>
          <select id="issue" className={styles.control} value={issueId} onChange={(e) => onIssue(e.target.value)} disabled={locked || issues.length === 0}>
            {issuesLoading && issues.length === 0 && <option value="">Loading issues…</option>}
            {!issuesLoading && issues.length === 0 && <option value="">No open issues</option>}
            {issues.map((i) => (
              <option key={i.id} value={i.id} disabled={i.outstanding_qty <= 0}>
                {i.sku.code} — {i.party.name}
                {i.outstanding_qty <= 0 ? " (fully received)" : ` (${i.outstanding_qty} left)`}
              </option>
            ))}
          </select>
        </div>
        <div className={styles.field}>
          <label htmlFor="qty" className={styles.label}>
            Quantity received
          </label>
          <input
            id="qty"
            className={`${styles.control} ${styles.num}`}
            type="number"
            min={1}
            value={qty}
            onChange={(e) => onQty(e.target.value)}
            disabled={locked}
          />
        </div>
        <div className={styles.field}>
          <label htmlFor="date" className={styles.label}>
            Received date
          </label>
          <input id="date" className={styles.control} type="date" value={receivedOn} onChange={(e) => onDate(e.target.value)} disabled={locked} />
        </div>
        <div className={`${styles.field} ${styles.formSubmit}`}>
          <button type="submit" className={styles.btnPrimary} disabled={submitting || locked || issues.length === 0}>
            {submitting && <span className={styles.spinner} aria-hidden="true" />}
            {submitting ? "Creating…" : "Create receipt"}
          </button>
        </div>
      </form>

      {selected && (
        <div className={styles.issueContext}>
          <Ctx label="SKU" value={selected.sku.code} />
          <Ctx label="Job-worker" value={selected.party.name} />
          <Ctx label="Issued" value={String(selected.issued_qty)} num />
          <Ctx label="Received" value={String(selected.received_qty)} num />
          <Ctx label="Outstanding" value={String(selected.outstanding_qty)} num />
        </div>
      )}

      <p className={styles.hint}>
        Each submit uses one idempotency key for the whole operation, saved before dispatch; retries reuse that key and
        payload, so a lost response never creates a second receipt. Receiving more than the outstanding quantity is
        rejected by the server.
      </p>
    </section>
  );
}

function Ctx({ label, value, num }: { label: string; value: string; num?: boolean }) {
  return (
    <span className={styles.ctxItem}>
      <span className={styles.ctxLabel}>{label}</span>
      <span className={`${styles.ctxValue} ${num ? styles.num : ""}`}>{value}</span>
    </span>
  );
}

export function PendingReceiptNotice(props: {
  op: PendingOp;
  submitting: boolean;
  onRetry: () => void;
  onDiscard: () => void;
}) {
  const { op, submitting, onRetry, onDiscard } = props;
  return (
    <div className={styles.pending} role="group" aria-labelledby="pending-title">
      <h3 id="pending-title" className={styles.pendingTitle}>
        Receipt confirmation pending
      </h3>
      <p className={styles.pendingText}>The request may have reached the server. Retry this receipt safely to check the result.</p>
      <div className={styles.pendingFacts}>
        <Ctx label="Issue" value={op.body.issue_id} />
        <Ctx label="Quantity" value={String(op.body.qty)} num />
        <Ctx label="Received date" value={op.body.received_on} num />
      </div>
      <div className={styles.pendingActions}>
        <button type="button" className={styles.btnPrimary} onClick={onRetry} disabled={submitting}>
          {submitting && <span className={styles.spinner} aria-hidden="true" />}
          {submitting ? "Retrying…" : "Retry this receipt"}
        </button>
        <button type="button" className={styles.btnSecondary} onClick={onDiscard} disabled={submitting}>
          Stop tracking locally
        </button>
      </div>
      <details className={styles.details}>
        <summary>Technical details</summary>
        <div>
          Idempotency key: <code className={styles.mono}>{op.key}</code>
        </div>
        <div>Stop tracking only hides this here; it does not cancel a receipt already recorded on the server.</div>
      </details>
    </div>
  );
}

export function VoucherSection(props: {
  id: string;
  title: string;
  description: string;
  emptyText: string;
  status: Status;
  vouchers: Voucher[];
  retrying: Record<string, boolean>;
  onRetry: (id: string) => void;
}) {
  const { id, title, description, emptyText, status, vouchers, retrying, onRetry } = props;
  return (
    <section id={id} className={styles.section} aria-labelledby={`${id}-title`}>
      <div className={styles.sectionHead}>
        <h2 id={`${id}-title`} className={styles.sectionTitle}>
          {title}
        </h2>
        <span className={`${styles.countBadge} ${styles.num} ${STATUS_STYLE[status]}`}>{vouchers.length}</span>
      </div>
      <p className={styles.sectionDesc}>{description}</p>
      {vouchers.length === 0 ? (
        <p className={styles.empty}>{emptyText}</p>
      ) : (
        <ul className={styles.voucherList}>
          {vouchers.map((v) => (
            <VoucherRow key={v.id} voucher={v} retrying={!!retrying[v.id]} onRetry={onRetry} />
          ))}
        </ul>
      )}
    </section>
  );
}

export function VoucherRow({ voucher, retrying, onRetry }: { voucher: Voucher; retrying: boolean; onRetry: (id: string) => void }) {
  const v = voucher;
  return (
    <li className={styles.voucherRow}>
      <div>
        <div className={`${styles.amount} ${styles.num}`}>{formatPaise(v.amount_paise)}</div>
        <span className={`${styles.pill} ${STATUS_STYLE[v.status]}`}>{STATUS_WORD[v.status]}</span>
      </div>
      <div className={styles.voucherMeta}>
        <Meta label="Sync attempts" value={String(v.attempts)} num />
        <Meta label="Last updated" value={formatTime(v.updated_at)} />
        <Meta label="Voucher" value={v.id} mono />
        <Meta label="Source" value={v.source_id} mono />
        <Meta label="Client ref" value={v.client_ref} mono />
        {v.external_ref && <Meta label="External ref" value={v.external_ref} mono />}
      </div>
      <div className={styles.rowActions}>
        {v.status === "failed" && (
          <button type="button" className={styles.btnSecondary} onClick={() => onRetry(v.id)} disabled={retrying}>
            {retrying && <span className={styles.spinner} aria-hidden="true" />}
            {retrying ? "Retrying…" : "Retry sync"}
          </button>
        )}
      </div>
      {v.last_error && (
        <div className={styles.lastError}>
          <span className={styles.metaLabel}>Last error: </span>
          {v.last_error}
        </div>
      )}
    </li>
  );
}

function Meta({ label, value, num, mono }: { label: string; value: string; num?: boolean; mono?: boolean }) {
  return (
    <span className={styles.metaItem}>
      <span className={styles.metaLabel}>{label}:</span>
      <span className={`${styles.metaValue} ${num ? styles.num : ""} ${mono ? styles.mono : ""}`}>{value}</span>
    </span>
  );
}

export function Skeleton(): ReactNode {
  return (
    <div className={styles.skeleton} aria-hidden="true">
      <div className={styles.skelRow} />
      <div className={styles.skelRow} />
      <div className={styles.skelRow} />
    </div>
  );
}
