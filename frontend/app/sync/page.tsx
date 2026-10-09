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

const inr = new Intl.NumberFormat("en-IN", { style: "currency", currency: "INR" });

function formatPaise(paise: number): string {
  return inr.format(paise / 100);
}

function formatTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

const GROUPS: { key: Status; title: string; color: string }[] = [
  { key: "pending", title: "Pending", color: "#b45309" },
  { key: "pushed", title: "Pushed", color: "#15803d" },
  { key: "failed", title: "Failed", color: "#b91c1c" },
];

export default function SyncPage() {
  const [vouchers, setVouchers] = useState<Voucher[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [retrying, setRetrying] = useState<Record<string, boolean>>({});
  const [feedback, setFeedback] = useState<{ kind: "ok" | "err"; msg: string } | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const inFlight = useRef(false);

  const load = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    try {
      const res = await fetch("/api/vouchers", { cache: "no-store" });
      if (!res.ok) {
        throw new Error(`backend returned ${res.status}`);
      }
      const data = await res.json();
      setVouchers(Array.isArray(data.vouchers) ? data.vouchers : []);
      setError(null);
      setLastUpdated(new Date());
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to load vouchers");
    } finally {
      setLoading(false);
      inFlight.current = false;
    }
  }, []);

  useEffect(() => {
    load();
    const id = setInterval(load, 4000);
    return () => clearInterval(id);
  }, [load]);

  const retry = useCallback(
    async (voucherId: string) => {
      setRetrying((r) => ({ ...r, [voucherId]: true }));
      setFeedback(null);
      try {
        const res = await fetch(`/api/vouchers/${voucherId}/retry`, { method: "POST" });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) {
          throw new Error(body?.error ?? `retry failed (${res.status})`);
        }
        setFeedback({ kind: "ok", msg: `Voucher re-queued for sync.` });
        await load();
      } catch (e) {
        setFeedback({ kind: "err", msg: e instanceof Error ? e.message : "retry failed" });
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
  for (const v of vouchers ?? []) {
    if (grouped[v.status]) grouped[v.status].push(v);
  }

  return (
    <main style={{ maxWidth: 980, margin: "0 auto", padding: "24px 16px" }}>
      <header style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", flexWrap: "wrap", gap: 12 }}>
        <div>
          <h1 style={{ margin: 0, fontSize: 24 }}>Sutra Mini — Voucher Sync</h1>
          <p style={{ margin: "4px 0 0", color: "#555", fontSize: 14 }}>
            {lastUpdated ? `Last updated ${lastUpdated.toLocaleTimeString()}` : "Loading…"}
          </p>
        </div>
        <button onClick={() => load()} style={btnStyle} disabled={loading && vouchers === null}>
          Refresh
        </button>
      </header>

      {feedback && (
        <div
          role="status"
          style={{
            margin: "16px 0",
            padding: "10px 14px",
            borderRadius: 6,
            background: feedback.kind === "ok" ? "#ecfdf5" : "#fef2f2",
            color: feedback.kind === "ok" ? "#065f46" : "#991b1b",
            border: `1px solid ${feedback.kind === "ok" ? "#a7f3d0" : "#fecaca"}`,
          }}
        >
          {feedback.msg}
        </div>
      )}

      {error && (
        <div
          role="alert"
          style={{ margin: "16px 0", padding: "10px 14px", borderRadius: 6, background: "#fef2f2", color: "#991b1b", border: "1px solid #fecaca" }}
        >
          Could not reach the API: {error}. Retrying automatically…
        </div>
      )}

      {loading && vouchers === null && <p style={{ color: "#555" }}>Loading vouchers…</p>}

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
                  <li key={v.id} style={cardStyle}>
                    <div style={{ display: "flex", justifyContent: "space-between", gap: 12, flexWrap: "wrap" }}>
                      <div>
                        <div style={{ fontWeight: 600 }}>{formatPaise(v.amount_paise)}</div>
                        <div style={{ fontSize: 12, color: "#555" }}>
                          {v.type} · source {v.source_id.slice(0, 8)}…
                        </div>
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
                        <button onClick={() => retry(v.id)} disabled={!!retrying[v.id]} style={btnStyle}>
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

const btnStyle: React.CSSProperties = {
  background: "#1d4ed8",
  color: "#fff",
  border: "none",
  borderRadius: 6,
  padding: "8px 16px",
  fontSize: 14,
  cursor: "pointer",
};

const cardStyle: React.CSSProperties = {
  background: "#fff",
  border: "1px solid #e5e7eb",
  borderRadius: 8,
  padding: 14,
  marginTop: 10,
};
