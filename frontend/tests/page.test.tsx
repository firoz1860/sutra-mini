import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import SyncPage from "../app/sync/page";

type PostCall = { key: string; body: { issue_id: string; qty: number; received_on: string }; auth: string | null };
type Call = { url: string; method: string; auth: string | null };

type Deferred = { promise: Promise<unknown>; resolve: (v: unknown) => void };

type Server = {
  authed: boolean;
  configured: boolean;
  password: string;
  receipts: Map<string, unknown>;
  issues: unknown[];
  postCalls: PostCall[];
  calls: Call[];
  dropNextPost: boolean;
  malformedNextPost: boolean;
  stallVouchers: boolean;
  deferVouchersOnce: Deferred | null;
};

let server: Server;
const TOKEN = "tok-test-1";

function resp(status: number, obj: unknown) {
  return Promise.resolve({ status, text: async () => JSON.stringify(obj), json: async () => obj });
}
function textResp(status: number, text: string) {
  return Promise.resolve({
    status,
    text: async () => text,
    json: async () => JSON.parse(text),
  });
}
function networkError() {
  return Promise.reject(new TypeError("Failed to fetch"));
}
function stallResp(signal: AbortSignal) {
  return Promise.resolve({
    status: 200,
    text: () =>
      new Promise<string>((_, reject) => {
        const err = Object.assign(new Error("aborted"), { name: "AbortError" });
        if (signal.aborted) reject(err);
        else signal.addEventListener("abort", () => reject(err), { once: true });
      }),
  });
}

function buildReceipt(key: string, body: PostCall["body"]) {
  return {
    receipt: { id: "r-" + key, issue_id: body.issue_id, qty: body.qty, received_on: body.received_on },
    voucher: { id: "v-" + key, client_ref: "jwr-r-" + key, status: "pending", amount_paise: body.qty * 100 },
    issue: { id: body.issue_id, issued_qty: 10, received_qty: body.qty, outstanding_qty: 10 - body.qty },
  };
}

function authOf(init: RequestInit): string | null {
  const h = (init.headers as Record<string, string>) || {};
  return h.Authorization ?? h.authorization ?? null;
}

function mockFetch(url: unknown, init: RequestInit): Promise<unknown> {
  const u = String(url);
  const auth = authOf(init);
  server.calls.push({ url: u, method: (init.method ?? "GET").toUpperCase(), auth });
  const signal = init.signal as AbortSignal;

  if (u.endsWith("/api/auth/session")) {
    return resp(200, { authenticated: server.authed && !!auth, configured: server.configured });
  }
  if (u.endsWith("/api/auth/login")) {
    const body = JSON.parse(init.body as string) as { password: string };
    if (body.password === server.password) return resp(200, { token: TOKEN, expires_at: "2099-01-01T00:00:00Z" });
    return resp(401, { error: "incorrect operator password", code: "invalid_credentials" });
  }
  if (u.endsWith("/api/vouchers")) {
    if (server.deferVouchersOnce) {
      const d = server.deferVouchersOnce;
      server.deferVouchersOnce = null;
      return d.promise;
    }
    if (server.stallVouchers) return stallResp(signal);
    return resp(200, { vouchers: [] });
  }
  if (u.endsWith("/api/issues")) {
    return resp(200, { issues: server.issues });
  }
  if (u.endsWith("/api/receipts")) {
    const key = (init.headers as Record<string, string>)["Idempotency-Key"];
    const body = JSON.parse(init.body as string) as PostCall["body"];
    server.postCalls.push({ key, body, auth });
    if (!server.receipts.has(key)) server.receipts.set(key, buildReceipt(key, body));
    if (server.dropNextPost) {
      server.dropNextPost = false;
      return networkError();
    }
    if (server.malformedNextPost) {
      server.malformedNextPost = false;
      return textResp(201, "<<not valid json>>");
    }
    return resp(201, server.receipts.get(key));
  }
  return resp(404, { error: "not found" });
}

beforeEach(() => {
  server = {
    authed: true,
    configured: true,
    password: "secret",
    receipts: new Map(),
    issues: [
      { id: "issue-1", issued_qty: 10, received_qty: 0, outstanding_qty: 10, sku: { code: "SKU-A", name: "Cotton" }, party: { name: "Weaver" } },
    ],
    postCalls: [],
    calls: [],
    dropNextPost: false,
    malformedNextPost: false,
    stallVouchers: false,
    deferVouchersOnce: null,
  };
  sessionStorage.clear();
  if (!globalThis.crypto || typeof globalThis.crypto.randomUUID !== "function") {
    let c = 0;
    // @ts-expect-error minimal polyfill for environments without crypto.randomUUID
    globalThis.crypto = { randomUUID: () => `uuid-${++c}` };
  }
  vi.stubGlobal("fetch", vi.fn(mockFetch));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

// Signed-in render: seed a stored token so the session effect restores it.
async function renderReady() {
  sessionStorage.setItem("sutra.operatorToken", TOKEN);
  render(<SyncPage />);
  await waitFor(() => {
    const btn = screen.getByRole("button", { name: /create receipt/i }) as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
  });
}

function clickCreate() {
  fireEvent.click(screen.getByRole("button", { name: /create receipt/i }));
}

function businessCalls() {
  return server.calls.filter((c) => /\/api\/(vouchers|issues|receipts)/.test(c.url));
}

describe("SyncPage authentication gating", () => {
  it("does not request protected data while signed out", async () => {
    server.authed = false; // no stored token, session reports not authenticated
    render(<SyncPage />);
    await waitFor(() => expect(screen.getByText(/operator sign-in required/i)).toBeTruthy());
    // No create-receipt form, and no protected endpoint was ever called.
    expect(screen.queryByRole("button", { name: /create receipt/i })).toBeNull();
    expect(businessCalls().length).toBe(0);
  });

  it("loads the dashboard after a successful login and forwards the bearer token", async () => {
    server.authed = false;
    render(<SyncPage />);
    await waitFor(() => expect(screen.getByText(/operator sign-in required/i)).toBeTruthy());

    fireEvent.change(screen.getByLabelText("Operator sign-in"), { target: { value: "secret" } });
    fireEvent.click(screen.getByRole("button", { name: /^sign in$/i }));

    await waitFor(() => {
      const btn = screen.getByRole("button", { name: /create receipt/i }) as HTMLButtonElement;
      expect(btn.disabled).toBe(false);
    });
    // Every protected request carried the operator bearer token.
    const protectedCalls = businessCalls();
    expect(protectedCalls.length).toBeGreaterThan(0);
    for (const c of protectedCalls) expect(c.auth).toBe(`Bearer ${TOKEN}`);
  });

  it("clears data and stops polling on sign-out", async () => {
    vi.useFakeTimers();
    sessionStorage.setItem("sutra.operatorToken", TOKEN);
    render(<SyncPage />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50);
    });
    expect(screen.queryByRole("button", { name: /create receipt/i })).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /sign out/i }));
    expect(screen.queryByRole("button", { name: /create receipt/i })).toBeNull();
    expect(screen.getByText(/operator sign-in required/i)).toBeTruthy();
    expect(sessionStorage.getItem("sutra.operatorToken")).toBeNull();

    const before = businessCalls().length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(12000); // several poll intervals
    });
    expect(businessCalls().length).toBe(before); // polling stopped
  });

  it("ignores a late voucher response that arrives after sign-out", async () => {
    let resolveVouchers!: (v: unknown) => void;
    const promise = new Promise<unknown>((res) => {
      resolveVouchers = res;
    });
    server.deferVouchersOnce = { promise, resolve: resolveVouchers };

    sessionStorage.setItem("sutra.operatorToken", TOKEN);
    render(<SyncPage />);
    // Wait until the first (deferred) vouchers poll is in flight.
    await waitFor(() => expect(server.calls.some((c) => c.url.endsWith("/api/vouchers"))).toBe(true));
    await waitFor(() => expect(screen.queryByRole("button", { name: /sign out/i })).not.toBeNull());

    fireEvent.click(screen.getByRole("button", { name: /sign out/i }));

    // The prior session's vouchers response resolves only now.
    await act(async () => {
      resolveVouchers(
        await resp(200, {
          vouchers: [
            {
              id: "v-late",
              type: "jobwork_receipt",
              source_id: "s",
              amount_paise: 999,
              status: "pending",
              attempts: 0,
              last_error: null,
              client_ref: "jwr-late",
              external_ref: null,
              next_attempt_at: "",
              created_at: "",
              updated_at: "",
            },
          ],
        }),
      );
      await Promise.resolve();
    });

    // Still signed out; the late data did not repopulate anything.
    expect(screen.getByText(/operator sign-in required/i)).toBeTruthy();
    expect(screen.queryByText(/jwr-late/i)).toBeNull();
  });
});

describe("SyncPage receipt reliability", () => {
  it("recovers a committed-but-lost receipt across reload without duplicating", async () => {
    server.dropNextPost = true;
    await renderReady();

    clickCreate();

    await waitFor(() => expect(screen.getByText(/may have reached the server/i)).toBeTruthy());
    expect(server.receipts.size).toBe(1); // server committed exactly once
    expect(sessionStorage.getItem("sutra.pendingReceipt")).toBeTruthy(); // op persisted
    const firstKey = server.postCalls[0].key;
    expect(screen.queryByText(/Receipt created/i)).toBeNull();

    // Simulate a reload: unmount, then mount a fresh component (sessionStorage persists).
    cleanup();
    sessionStorage.setItem("sutra.operatorToken", TOKEN);
    render(<SyncPage />);
    const retryBtn = await screen.findByRole("button", { name: /retry this receipt/i });

    fireEvent.click(retryBtn);
    await waitFor(() => expect(screen.getByText(/Receipt created/i)).toBeTruthy());

    const lastCall = server.postCalls[server.postCalls.length - 1];
    expect(lastCall.key).toBe(firstKey); // same idempotency key
    expect(lastCall.body).toEqual(server.postCalls[0].body); // exact same payload
    expect(lastCall.auth).toBe(`Bearer ${TOKEN}`); // authenticated
    expect(server.receipts.size).toBe(1); // still exactly one receipt/voucher
    expect(sessionStorage.getItem("sutra.pendingReceipt")).toBeNull(); // cleared on validated success
  });

  it("does not treat a malformed HTTP 201 as success", async () => {
    server.malformedNextPost = true;
    await renderReady();

    clickCreate();

    await waitFor(() => expect(screen.getByText(/malformed or incomplete/i)).toBeTruthy());
    expect(screen.queryByText(/Receipt created/i)).toBeNull();
    expect(sessionStorage.getItem("sutra.pendingReceipt")).toBeTruthy(); // op retained for safe retry
  });

  it("ignores repeated clicks and dispatches exactly one operation", async () => {
    await renderReady();
    const btn = screen.getByRole("button", { name: /create receipt/i });

    fireEvent.click(btn);
    fireEvent.click(btn);
    fireEvent.click(btn);

    await waitFor(() => expect(screen.getByText(/Receipt created/i)).toBeTruthy());
    expect(server.postCalls.length).toBe(1);
    expect(server.receipts.size).toBe(1);
  });

  it("times out a stalled response body and recovers on the next poll", async () => {
    vi.useFakeTimers();
    server.stallVouchers = true;
    sessionStorage.setItem("sutra.operatorToken", TOKEN);
    render(<SyncPage />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(8050); // let the session resolve, then the stalled body time out
    });
    expect(screen.getByText(/Could not reach the API/i)).toBeTruthy();

    server.stallVouchers = false;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(12000);
    });
    expect(screen.queryByText(/Could not reach the API/i)).toBeNull();
  });
});
