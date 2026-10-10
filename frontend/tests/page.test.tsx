import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import SyncPage from "../app/sync/page";

type PostCall = { key: string; body: { issue_id: string; qty: number; received_on: string } };

type Server = {
  receipts: Map<string, unknown>;
  issues: unknown[];
  postCalls: PostCall[];
  dropNextPost: boolean;
  malformedNextPost: boolean;
  stallVouchers: boolean;
};

let server: Server;

function resp(status: number, obj: unknown) {
  return Promise.resolve({ status, text: async () => JSON.stringify(obj) });
}
function textResp(status: number, text: string) {
  return Promise.resolve({ status, text: async () => text });
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

function mockFetch(url: unknown, init: RequestInit): Promise<unknown> {
  const u = String(url);
  const signal = init.signal as AbortSignal;
  if (u.endsWith("/api/vouchers")) {
    if (server.stallVouchers) return stallResp(signal);
    return resp(200, { vouchers: [] });
  }
  if (u.endsWith("/api/issues")) {
    return resp(200, { issues: server.issues });
  }
  if (u.endsWith("/api/receipts")) {
    const key = (init.headers as Record<string, string>)["Idempotency-Key"];
    const body = JSON.parse(init.body as string) as PostCall["body"];
    server.postCalls.push({ key, body });
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
    receipts: new Map(),
    issues: [
      { id: "issue-1", issued_qty: 10, received_qty: 0, outstanding_qty: 10, sku: { code: "SKU-A", name: "Cotton" }, party: { name: "Weaver" } },
    ],
    postCalls: [],
    dropNextPost: false,
    malformedNextPost: false,
    stallVouchers: false,
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

async function renderReady() {
  render(<SyncPage />);
  await waitFor(() => {
    const btn = screen.getByRole("button", { name: /create receipt/i }) as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
  });
}

function clickCreate() {
  fireEvent.click(screen.getByRole("button", { name: /create receipt/i }));
}

describe("SyncPage receipt reliability", () => {
  it("recovers a committed-but-lost receipt across reload without duplicating", async () => {
    server.dropNextPost = true;
    await renderReady();

    clickCreate();

    await waitFor(() => expect(screen.getByText(/resubmit safely with the same key/i)).toBeTruthy());
    expect(server.receipts.size).toBe(1); // server committed exactly once
    expect(sessionStorage.getItem("sutra.pendingReceipt")).toBeTruthy(); // op persisted
    const firstKey = server.postCalls[0].key;
    expect(screen.queryByText(/Receipt created/i)).toBeNull();

    // Simulate a reload: unmount, then mount a fresh component (sessionStorage persists).
    cleanup();
    render(<SyncPage />);
    const retryBtn = await screen.findByRole("button", { name: /^retry$/i });

    fireEvent.click(retryBtn);
    await waitFor(() => expect(screen.getByText(/Receipt created/i)).toBeTruthy());

    const lastCall = server.postCalls[server.postCalls.length - 1];
    expect(lastCall.key).toBe(firstKey); // same idempotency key
    expect(lastCall.body).toEqual(server.postCalls[0].body); // exact same payload
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
    render(<SyncPage />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(8000); // whole-response timeout fires on the stalled body
    });
    expect(screen.getByText(/Could not reach the API/i)).toBeTruthy();

    server.stallVouchers = false;
    await act(async () => {
      // Any still-in-flight stalled poll aborts after its 8s timeout (proving it never
      // permanently blocks polling), then a subsequent poll succeeds and clears the error.
      await vi.advanceTimersByTimeAsync(12000);
    });
    expect(screen.queryByText(/Could not reach the API/i)).toBeNull();
  });
});
