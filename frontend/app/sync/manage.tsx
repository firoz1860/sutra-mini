"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import styles from "./page.module.css";
import { formatPaise, type Feedback, type Issue } from "./ui";

export type Party = { id: string; name: string; phone: string };
export type SKU = { id: string; code: string; name: string; image_url: string };

type ApiResult = { status: number; data: unknown };

async function apiJSON(url: string, token: string | null, opts: RequestInit = {}): Promise<ApiResult> {
  const headers: Record<string, string> = { ...(opts.headers as Record<string, string> | undefined) };
  if (token) headers["Authorization"] = `Bearer ${token}`;
  const res = await fetch(url, { ...opts, headers, cache: "no-store", signal: AbortSignal.timeout(15000) });
  const text = await res.text();
  let data: unknown;
  if (text.trim().length > 0) {
    try {
      data = JSON.parse(text);
    } catch {
      /* ignore */
    }
  }
  return { status: res.status, data };
}

function errOf(data: unknown, fallback: string): string {
  const e = (data as { error?: string } | undefined)?.error;
  return typeof e === "string" && e.length > 0 ? e : fallback;
}

function todayLocal(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

function Banner({ feedback }: { feedback: Feedback | null }) {
  if (!feedback) return null;
  return (
    <div role="status" className={`${styles.feedback} ${feedback.kind === "ok" ? styles.feedbackOk : styles.feedbackErr}`}>
      {feedback.msg}
    </div>
  );
}

export function OperatorBar(props: {
  token: string | null;
  configured: boolean | null;
  onLogin: (password: string) => Promise<string | null>;
  onLogout: () => void;
}) {
  const { token, configured, onLogin, onLogout } = props;
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (token) {
    return (
      <div className={styles.opBar}>
        <span className={styles.opText}>
          <span className={styles.opDot} aria-hidden="true" /> Signed in as operator
        </span>
        <button type="button" className={styles.btnSecondary} onClick={onLogout}>
          Sign out
        </button>
      </div>
    );
  }

  return (
    <form
      className={styles.opBar}
      onSubmit={async (e) => {
        e.preventDefault();
        if (busy) return;
        setBusy(true);
        setError(null);
        const msg = await onLogin(password);
        setBusy(false);
        if (msg) setError(msg);
        else setPassword("");
      }}
    >
      <label htmlFor="op-pw" className={styles.opText}>
        Operator sign-in
      </label>
      <input
        id="op-pw"
        className={styles.control}
        type="password"
        autoComplete="current-password"
        placeholder={configured === false ? "auth not configured" : "operator password"}
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        disabled={busy}
      />
      <button type="submit" className={styles.btnSecondary} disabled={busy || password.length === 0}>
        {busy ? "Signing in…" : "Sign in"}
      </button>
      {error && (
        <span role="alert" className={styles.opError}>
          {error}
        </span>
      )}
    </form>
  );
}

function SignInPrompt({ what }: { what: string }) {
  return <p className={styles.empty}>Sign in as an operator (top right) to manage {what}.</p>;
}

function SectionError({ msg, onRetry }: { msg: string; onRetry: () => void }) {
  return (
    <div role="alert" className={styles.stale}>
      {msg}{" "}
      <button type="button" className={styles.linkBtn} onClick={onRetry}>
        Retry
      </button>
    </div>
  );
}

function Thumb({ url, alt }: { url?: string; alt: string }) {
  const [broken, setBroken] = useState(false);
  if (!url || broken) {
    return (
      <span className={styles.thumbFallback} aria-hidden="true">
        {alt.slice(0, 2).toUpperCase()}
      </span>
    );
  }
  
  return <img className={styles.thumb} src={url} alt="" onError={() => setBroken(true)} loading="lazy" />;
}

export function JobWorkersTab({ token, onChanged, onUnauthorized }: { token: string | null; onChanged: () => void; onUnauthorized: () => void }) {
  const [parties, setParties] = useState<Party[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [busy, setBusy] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editPhone, setEditPhone] = useState("");

  const load = useCallback(async () => {
    if (!token) return;
    try {
      const { status, data } = await apiJSON("/api/parties", token);
      if (status === 401) {
        onUnauthorized();
        return;
      }
      if (status !== 200) throw new Error(errOf(data, `request failed (${status})`));
      setParties(((data as { parties?: Party[] }).parties ?? []) as Party[]);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "could not load job-workers");
    }
  }, [token, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  if (!token) return <SignInPrompt what="job-workers" />;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setFeedback(null);
    try {
      const { status, data } = await apiJSON("/api/parties", token, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ name, phone }),
      });
      if (status === 401) {
        onUnauthorized();
        return;
      }
      if (status === 201) {
        setName("");
        setPhone("");
        setFeedback({ kind: "ok", msg: "Job-worker added." });
        await load();
        onChanged();
      } else {
        setFeedback({ kind: "err", msg: errOf(data, `could not add job-worker (${status})`) });
      }
    } catch {
      setFeedback({ kind: "err", msg: "Network problem — the job-worker may not have been saved. Check the list and retry if needed." });
    } finally {
      setBusy(false);
    }
  };

  const saveEdit = async (id: string) => {
    setFeedback(null);
    try {
      const { status, data } = await apiJSON(`/api/parties/${encodeURIComponent(id)}`, token, {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ name: editName, phone: editPhone }),
      });
      if (status === 401) {
        onUnauthorized();
        return;
      }
      if (status === 200) {
        setEditId(null);
        setFeedback({ kind: "ok", msg: "Job-worker updated." });
        await load();
        onChanged();
      } else {
        setFeedback({ kind: "err", msg: errOf(data, `could not update (${status})`) });
      }
    } catch {
      setFeedback({ kind: "err", msg: "Network problem while saving." });
    }
  };

  return (
    <div>
      <section className={styles.card} aria-labelledby="worker-form-title">
        <h2 id="worker-form-title" className={styles.cardTitle}>
          Add a job-worker
        </h2>
        <p className={styles.cardDesc}>Name is required. Phone is optional and stored as a contact detail.</p>
        <form onSubmit={submit} className={styles.mForm}>
          <div className={styles.field}>
            <label htmlFor="w-name" className={styles.label}>
              Name
            </label>
            <input id="w-name" className={styles.control} value={name} maxLength={200} onChange={(e) => setName(e.target.value)} disabled={busy} />
          </div>
          <div className={styles.field}>
            <label htmlFor="w-phone" className={styles.label}>
              Phone (optional)
            </label>
            <input id="w-phone" className={styles.control} type="tel" value={phone} maxLength={40} onChange={(e) => setPhone(e.target.value)} disabled={busy} />
          </div>
          <div className={`${styles.field} ${styles.formSubmit}`}>
            <button type="submit" className={styles.btnPrimary} disabled={busy || name.trim().length === 0}>
              {busy ? "Adding…" : "Add job-worker"}
            </button>
          </div>
        </form>
      </section>

      <Banner feedback={feedback} />

      <section className={styles.section} aria-labelledby="worker-list-title">
        <div className={styles.sectionHead}>
          <h2 id="worker-list-title" className={styles.sectionTitle}>
            Job-workers
          </h2>
        </div>
        {error && <SectionError msg={`Could not load job-workers (${error}).`} onRetry={() => void load()} />}
        {parties === null && !error ? (
          <p className={styles.empty}>Loading…</p>
        ) : parties && parties.length === 0 ? (
          <p className={styles.empty}>No job-workers yet. Add the first one above.</p>
        ) : (
          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Phone</th>
                  <th aria-label="actions" />
                </tr>
              </thead>
              <tbody>
                {(parties ?? []).map((p) =>
                  editId === p.id ? (
                    <tr key={p.id}>
                      <td>
                        <input className={styles.control} value={editName} onChange={(e) => setEditName(e.target.value)} />
                      </td>
                      <td>
                        <input className={styles.control} type="tel" value={editPhone} onChange={(e) => setEditPhone(e.target.value)} />
                      </td>
                      <td className={styles.rowActions}>
                        <button type="button" className={styles.btnPrimary} onClick={() => void saveEdit(p.id)}>
                          Save
                        </button>
                        <button type="button" className={styles.btnSecondary} onClick={() => setEditId(null)}>
                          Cancel
                        </button>
                      </td>
                    </tr>
                  ) : (
                    <tr key={p.id}>
                      <td>{p.name}</td>
                      <td className={styles.mono}>{p.phone || "—"}</td>
                      <td className={styles.rowActions}>
                        <button
                          type="button"
                          className={styles.btnSecondary}
                          onClick={() => {
                            setEditId(p.id);
                            setEditName(p.name);
                            setEditPhone(p.phone);
                          }}
                        >
                          Edit
                        </button>
                      </td>
                    </tr>
                  ),
                )}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

export function SKUsTab({ token, onChanged, onUnauthorized }: { token: string | null; onChanged: () => void; onUnauthorized: () => void }) {
  const [skus, setSKUs] = useState<SKU[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [imageURL, setImageURL] = useState("");
  const [busy, setBusy] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editImage, setEditImage] = useState("");

  const load = useCallback(async () => {
    if (!token) return;
    try {
      const { status, data } = await apiJSON("/api/skus", token);
      if (status === 401) {
        onUnauthorized();
        return;
      }
      if (status !== 200) throw new Error(errOf(data, `request failed (${status})`));
      setSKUs(((data as { skus?: SKU[] }).skus ?? []) as SKU[]);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "could not load SKUs");
    }
  }, [token, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  if (!token) return <SignInPrompt what="SKUs" />;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setFeedback(null);
    try {
      const { status, data } = await apiJSON("/api/skus", token, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ code, name, image_url: imageURL }),
      });
      if (status === 401) {
        onUnauthorized();
        return;
      }
      if (status === 201) {
        setCode("");
        setName("");
        setImageURL("");
        setFeedback({ kind: "ok", msg: "SKU added." });
        await load();
        onChanged();
      } else if (status === 409) {
        setFeedback({ kind: "err", msg: "A SKU with that code already exists." });
      } else {
        setFeedback({ kind: "err", msg: errOf(data, `could not add SKU (${status})`) });
      }
    } catch {
      setFeedback({ kind: "err", msg: "Network problem — the SKU may not have been saved. Check the list and retry if needed." });
    } finally {
      setBusy(false);
    }
  };

  const saveEdit = async (id: string) => {
    setFeedback(null);
    try {
      const { status, data } = await apiJSON(`/api/skus/${encodeURIComponent(id)}`, token, {
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ name: editName, image_url: editImage }),
      });
      if (status === 401) {
        onUnauthorized();
        return;
      }
      if (status === 200) {
        setEditId(null);
        setFeedback({ kind: "ok", msg: "SKU updated." });
        await load();
        onChanged();
      } else {
        setFeedback({ kind: "err", msg: errOf(data, `could not update (${status})`) });
      }
    } catch {
      setFeedback({ kind: "err", msg: "Network problem while saving." });
    }
  };

  return (
    <div>
      <section className={styles.card} aria-labelledby="sku-form-title">
        <h2 id="sku-form-title" className={styles.cardTitle}>
          Add a SKU
        </h2>
        <p className={styles.cardDesc}>Code is required and unique (normalized to upper-case). Image URL is optional.</p>
        <form onSubmit={submit} className={styles.mForm}>
          <div className={styles.field}>
            <label htmlFor="s-code" className={styles.label}>
              Code
            </label>
            <input id="s-code" className={`${styles.control} ${styles.mono}`} value={code} maxLength={64} onChange={(e) => setCode(e.target.value)} disabled={busy} />
          </div>
          <div className={styles.field}>
            <label htmlFor="s-name" className={styles.label}>
              Name
            </label>
            <input id="s-name" className={styles.control} value={name} maxLength={200} onChange={(e) => setName(e.target.value)} disabled={busy} />
          </div>
          <div className={styles.field}>
            <label htmlFor="s-image" className={styles.label}>
              Image URL (optional)
            </label>
            <input id="s-image" className={styles.control} type="url" value={imageURL} maxLength={2048} onChange={(e) => setImageURL(e.target.value)} disabled={busy} />
          </div>
          <div className={`${styles.field} ${styles.formSubmit}`}>
            <button type="submit" className={styles.btnPrimary} disabled={busy || code.trim().length === 0 || name.trim().length === 0}>
              {busy ? "Adding…" : "Add SKU"}
            </button>
          </div>
        </form>
      </section>

      <Banner feedback={feedback} />

      <section className={styles.section} aria-labelledby="sku-list-title">
        <div className={styles.sectionHead}>
          <h2 id="sku-list-title" className={styles.sectionTitle}>
            SKUs
          </h2>
        </div>
        {error && <SectionError msg={`Could not load SKUs (${error}).`} onRetry={() => void load()} />}
        {skus === null && !error ? (
          <p className={styles.empty}>Loading…</p>
        ) : skus && skus.length === 0 ? (
          <p className={styles.empty}>No SKUs yet. Add the first one above.</p>
        ) : (
          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th aria-label="image" />
                  <th>Code</th>
                  <th>Name</th>
                  <th aria-label="actions" />
                </tr>
              </thead>
              <tbody>
                {(skus ?? []).map((s) =>
                  editId === s.id ? (
                    <tr key={s.id}>
                      <td>
                        <Thumb url={editImage} alt={s.code} />
                      </td>
                      <td className={styles.mono}>{s.code}</td>
                      <td>
                        <input className={styles.control} value={editName} onChange={(e) => setEditName(e.target.value)} />
                        <input className={styles.control} type="url" placeholder="image URL" value={editImage} onChange={(e) => setEditImage(e.target.value)} />
                      </td>
                      <td className={styles.rowActions}>
                        <button type="button" className={styles.btnPrimary} onClick={() => void saveEdit(s.id)}>
                          Save
                        </button>
                        <button type="button" className={styles.btnSecondary} onClick={() => setEditId(null)}>
                          Cancel
                        </button>
                      </td>
                    </tr>
                  ) : (
                    <tr key={s.id}>
                      <td>
                        <Thumb url={s.image_url} alt={s.code} />
                      </td>
                      <td className={styles.mono}>{s.code}</td>
                      <td>{s.name}</td>
                      <td className={styles.rowActions}>
                        <button
                          type="button"
                          className={styles.btnSecondary}
                          onClick={() => {
                            setEditId(s.id);
                            setEditName(s.name);
                            setEditImage(s.image_url);
                          }}
                        >
                          Edit
                        </button>
                      </td>
                    </tr>
                  ),
                )}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}

type IssuePayload = { party_id: string; sku_id: string; qty: number; issued_on: string; unit_rate_rupees: string };

export function IssuesTab(props: { token: string | null; issues: Issue[]; onChanged: () => void; onUnauthorized: () => void }) {
  const { token, issues, onChanged, onUnauthorized } = props;
  const [parties, setParties] = useState<Party[]>([]);
  const [skus, setSKUs] = useState<SKU[]>([]);
  const [refError, setRefError] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<Feedback | null>(null);

  const [partyId, setPartyId] = useState("");
  const [skuId, setSKUId] = useState("");
  const [qty, setQty] = useState("1");
  const [issuedOn, setIssuedOn] = useState(todayLocal());
  const [rate, setRate] = useState("");
  const [busy, setBusy] = useState(false);
  const idemRef = useRef<{ fp: string; key: string } | null>(null);
  const busyRef = useRef(false);

  const [openIssue, setOpenIssue] = useState<string | null>(null);

  const loadRefs = useCallback(async () => {
    if (!token) return;
    try {
      const [pr, sr] = await Promise.all([apiJSON("/api/parties", token), apiJSON("/api/skus", token)]);
      if (pr.status === 401 || sr.status === 401) {
        onUnauthorized();
        return;
      }
      if (pr.status !== 200) throw new Error(errOf(pr.data, "parties failed"));
      if (sr.status !== 200) throw new Error(errOf(sr.data, "skus failed"));
      setParties(((pr.data as { parties?: Party[] }).parties ?? []) as Party[]);
      setSKUs(((sr.data as { skus?: SKU[] }).skus ?? []) as SKU[]);
      setRefError(null);
    } catch (e) {
      setRefError(e instanceof Error ? e.message : "could not load form options");
    }
  }, [token, onUnauthorized]);

  useEffect(() => {
    void loadRefs();
  }, [loadRefs]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busyRef.current || busy) return;
    if (!partyId || !skuId) {
      setFeedback({ kind: "err", msg: "Choose a job-worker and a SKU." });
      return;
    }
    const n = Number(qty);
    if (!Number.isInteger(n) || n <= 0) {
      setFeedback({ kind: "err", msg: "Enter a whole issued quantity greater than zero." });
      return;
    }
    if (!/^\d+(\.\d{1,2})?$/.test(rate.trim())) {
      setFeedback({ kind: "err", msg: "Enter a rate in rupees with at most two decimals (e.g. 25 or 25.50)." });
      return;
    }
    const payload: IssuePayload = { party_id: partyId, sku_id: skuId, qty: n, issued_on: issuedOn, unit_rate_rupees: rate.trim() };
    const fp = `${payload.party_id}|${payload.sku_id}|${payload.qty}|${payload.issued_on}|${payload.unit_rate_rupees}`;
    if (!idemRef.current || idemRef.current.fp !== fp) idemRef.current = { fp, key: crypto.randomUUID() };
    const key = idemRef.current.key;

    busyRef.current = true;
    setBusy(true);
    setFeedback(null);
    try {
      const { status, data } = await apiJSON("/api/jobwork/issues", token, {
        method: "POST",
        headers: { "content-type": "application/json", "Idempotency-Key": key },
        body: JSON.stringify(payload),
      });
      if (status === 401) {
        onUnauthorized();
        return;
      }
      if (status === 201) {
        idemRef.current = null;
        setQty("1");
        setRate("");
        setFeedback({ kind: "ok", msg: "Issue created. It is now selectable for receipts in the overview." });
        onChanged();
      } else if (status === 400 || status === 404 || status === 409) {
        idemRef.current = null;
        setFeedback({ kind: "err", msg: errOf(data, `rejected (HTTP ${status})`) });
      } else {
        setFeedback({
          kind: "err",
          msg: `Unclear outcome (HTTP ${status}). Retry safely — the same idempotency key is reused, so a duplicate issue will not be created.`,
        });
      }
    } catch {
      setFeedback({
        kind: "err",
        msg: "Network problem. Retry safely — the same idempotency key is reused, so a duplicate issue will not be created.",
      });
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  };

  return (
    <div>
      {token ? (
        <section className={styles.card} aria-labelledby="issue-form-title">
          <h2 id="issue-form-title" className={styles.cardTitle}>
            Create a job-work issue
          </h2>
          <p className={styles.cardDesc}>
            Quantity is in whole units. The rate is per unit in rupees and is stored as a valuation snapshot in integer paise.
          </p>
          {refError && <SectionError msg={`Could not load job-workers/SKUs (${refError}).`} onRetry={() => void loadRefs()} />}
          <form onSubmit={submit} className={styles.mForm}>
            <div className={styles.field}>
              <label htmlFor="i-party" className={styles.label}>
                Job-worker
              </label>
              <select id="i-party" className={styles.control} value={partyId} onChange={(e) => setPartyId(e.target.value)} disabled={busy}>
                <option value="">Select a job-worker…</option>
                {parties.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </div>
            <div className={styles.field}>
              <label htmlFor="i-sku" className={styles.label}>
                SKU
              </label>
              <select id="i-sku" className={styles.control} value={skuId} onChange={(e) => setSKUId(e.target.value)} disabled={busy}>
                <option value="">Select a SKU…</option>
                {skus.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.code} — {s.name}
                  </option>
                ))}
              </select>
            </div>
            <div className={styles.field}>
              <label htmlFor="i-qty" className={styles.label}>
                Issued quantity
              </label>
              <input id="i-qty" className={`${styles.control} ${styles.num}`} type="number" min={1} value={qty} onChange={(e) => setQty(e.target.value)} disabled={busy} />
            </div>
            <div className={styles.field}>
              <label htmlFor="i-date" className={styles.label}>
                Issued date
              </label>
              <input id="i-date" className={styles.control} type="date" value={issuedOn} onChange={(e) => setIssuedOn(e.target.value)} disabled={busy} />
            </div>
            <div className={styles.field}>
              <label htmlFor="i-rate" className={styles.label}>
                Rate per unit (₹)
              </label>
              <input id="i-rate" className={`${styles.control} ${styles.num}`} inputMode="decimal" placeholder="25.00" value={rate} onChange={(e) => setRate(e.target.value)} disabled={busy} />
            </div>
            <div className={`${styles.field} ${styles.formSubmit}`}>
              <button type="submit" className={styles.btnPrimary} disabled={busy}>
                {busy ? "Creating…" : "Create issue"}
              </button>
            </div>
          </form>
        </section>
      ) : (
        <SignInPrompt what="job-work issues" />
      )}

      <Banner feedback={feedback} />

      <section className={styles.section} aria-labelledby="issue-list-title">
        <div className={styles.sectionHead}>
          <h2 id="issue-list-title" className={styles.sectionTitle}>
            Job-work issues
          </h2>
        </div>
        {issues.length === 0 ? (
          <p className={styles.empty}>No issues yet.</p>
        ) : (
          <ul className={styles.issueList}>
            {issues.map((i) => (
              <li key={i.id} className={styles.issueCard}>
                <div className={styles.issueHead}>
                  <Thumb url={i.sku.image_url} alt={i.sku.code} />
                  <div>
                    <div className={styles.issueName}>
                      <span className={styles.mono}>{i.sku.code}</span> · {i.sku.name}
                    </div>
                    <div className={styles.subtle}>
                      {i.party.name}
                      {i.party.phone ? <span className={styles.mono}> · {i.party.phone}</span> : null}
                    </div>
                  </div>
                </div>
                <div className={styles.issueContext}>
                  <Field label="Issued" value={String(i.issued_qty)} num />
                  <Field label="Received" value={String(i.received_qty)} num />
                  <Field label="Outstanding" value={String(i.outstanding_qty)} num />
                  {typeof i.unit_rate_paise === "number" && <Field label="Rate/unit" value={formatPaise(i.unit_rate_paise)} num />}
                  {i.issued_on && <Field label="Issued on" value={i.issued_on} num />}
                </div>
                <div className={styles.rowActions}>
                  <button
                    type="button"
                    className={styles.linkBtn}
                    aria-expanded={openIssue === i.id}
                    onClick={() => setOpenIssue(openIssue === i.id ? null : i.id)}
                  >
                    {openIssue === i.id ? "Hide receipts" : "View receipts"}
                  </button>
                </div>
                {openIssue === i.id && <ReceiptHistory issueId={i.id} token={token} onUnauthorized={onUnauthorized} />}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function Field({ label, value, num }: { label: string; value: string; num?: boolean }) {
  return (
    <span className={styles.ctxItem}>
      <span className={styles.ctxLabel}>{label}</span>
      <span className={`${styles.ctxValue} ${num ? styles.num : ""}`}>{value}</span>
    </span>
  );
}

type HistoryRow = {
  receipt_id: string;
  received_on: string;
  qty: number;
  voucher: { id: string; client_ref: string; amount_paise: number; status: string; attempts: number; last_error: string | null } | null;
};

const HISTORY_LIMIT = 20;

export function ReceiptHistory({ issueId, token, onUnauthorized }: { issueId: string; token: string | null; onUnauthorized: () => void }) {
  const [rows, setRows] = useState<HistoryRow[] | null>(null);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const { status, data } = await apiJSON(
        `/api/jobwork/receipts?issue_id=${encodeURIComponent(issueId)}&limit=${HISTORY_LIMIT}&offset=${offset}`,
        token,
      );
      if (status === 401) {
        onUnauthorized();
        return;
      }
      if (status !== 200) throw new Error(errOf(data, `request failed (${status})`));
      const d = data as { receipts?: HistoryRow[]; total?: number };
      setRows(d.receipts ?? []);
      setTotal(d.total ?? 0);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "could not load receipts");
    }
  }, [issueId, offset, token, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  if (error) return <SectionError msg={`Could not load receipt history (${error}).`} onRetry={() => void load()} />;
  if (rows === null) return <p className={styles.empty}>Loading receipts…</p>;
  if (rows.length === 0) return <p className={styles.empty}>No receipts recorded against this issue yet.</p>;

  const page = Math.floor(offset / HISTORY_LIMIT) + 1;
  const pages = Math.max(1, Math.ceil(total / HISTORY_LIMIT));

  return (
    <div className={styles.tableWrap}>
      <table className={styles.table}>
        <thead>
          <tr>
            <th>Received</th>
            <th>Qty</th>
            <th>Voucher</th>
            <th>Amount</th>
            <th>Status</th>
            <th>Attempts</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.receipt_id}>
              <td className={styles.num}>{r.received_on}</td>
              <td className={styles.num}>{r.qty}</td>
              <td className={styles.mono}>{r.voucher ? r.voucher.client_ref : "—"}</td>
              <td className={styles.num}>{r.voucher ? formatPaise(r.voucher.amount_paise) : "—"}</td>
              <td>
                {r.voucher ? <span className={styles.pill}>{cap(r.voucher.status)}</span> : "—"}
                {r.voucher?.last_error ? <div className={styles.lastError}>{r.voucher.last_error}</div> : null}
              </td>
              <td className={styles.num}>{r.voucher ? r.voucher.attempts : "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {total > HISTORY_LIMIT && (
        <div className={styles.pager}>
          <button type="button" className={styles.btnSecondary} disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - HISTORY_LIMIT))}>
            Previous
          </button>
          <span className={styles.subtle}>
            Page {page} of {pages} · {total} total
          </span>
          <button type="button" className={styles.btnSecondary} disabled={offset + HISTORY_LIMIT >= total} onClick={() => setOffset(offset + HISTORY_LIMIT)}>
            Next
          </button>
        </div>
      )}
    </div>
  );
}

function cap(s: string): string {
  return s.length === 0 ? s : s[0].toUpperCase() + s.slice(1);
}
