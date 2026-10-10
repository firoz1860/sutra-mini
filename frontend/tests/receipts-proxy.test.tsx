import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NextRequest } from "next/server";
import { GET } from "../app/api/jobwork/receipts/route";



type Captured = { url: string; headers: Record<string, string>; cache?: RequestCache };
let captured: Captured | null;
let backendStatus: number;
let backendBody: string;

function headerMap(init: RequestInit): Record<string, string> {
  const h = init.headers;
  if (!h) return {};
  if (h instanceof Headers) return Object.fromEntries(h.entries());
  if (Array.isArray(h)) return Object.fromEntries(h);
  return { ...(h as Record<string, string>) };
}

beforeEach(() => {
  captured = null;
  backendStatus = 200;
  backendBody = JSON.stringify({
    receipts: [
      {
        receipt_id: "r-1",
        received_on: "2026-02-01",
        qty: 3,
        voucher: { id: "v-1", client_ref: "jwr-r-1", amount_paise: 300, status: "pushed", attempts: 1, last_error: null },
      },
    ],
    total: 1,
    limit: 20,
    offset: 0,
  });
  vi.stubGlobal(
    "fetch",
    vi.fn((url: unknown, init: RequestInit) => {
      captured = { url: String(url), headers: headerMap(init), cache: init.cache };
      return Promise.resolve({ status: backendStatus, text: async () => backendBody });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function call(query: string, headers?: Record<string, string>) {
  return GET(new NextRequest(`http://localhost:3000/api/jobwork/receipts${query}`, headers ? { headers } : undefined));
}

describe("jobwork/receipts proxy (GET)", () => {
  it("forwards the incoming bearer token to the backend unchanged", async () => {
    await call("?issue_id=issue-1&limit=20&offset=0", { authorization: "Bearer tok-abc" });
    expect(captured).not.toBeNull();
    const forwarded = captured!.headers.authorization ?? captured!.headers.Authorization;
    expect(forwarded).toBe("Bearer tok-abc");
  });

  it("preserves issue_id, limit, and offset query parameters", async () => {
    await call("?issue_id=issue-42&limit=5&offset=10", { authorization: "Bearer tok-abc" });
    const target = new URL(captured!.url);
    expect(target.pathname).toBe("/jobwork/receipts");
    expect(target.searchParams.get("issue_id")).toBe("issue-42");
    expect(target.searchParams.get("limit")).toBe("5");
    expect(target.searchParams.get("offset")).toBe("10");
  });

  it("returns the backend receipt + voucher history on success and stays non-cacheable", async () => {
    const res = await call("?issue_id=issue-1&limit=20&offset=0", { authorization: "Bearer tok-abc" });
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("no-store");
    const data = JSON.parse(await res.text()) as {
      receipts: { receipt_id: string; qty: number; voucher: { client_ref: string; status: string } | null }[];
      total: number;
    };
    expect(data.total).toBe(1);
    expect(data.receipts[0].receipt_id).toBe("r-1");
    expect(data.receipts[0].qty).toBe(3);
    expect(data.receipts[0].voucher?.client_ref).toBe("jwr-r-1");
    expect(data.receipts[0].voucher?.status).toBe("pushed");
    expect(captured!.cache).toBe("no-store");
  });

  it("does not inject any Authorization when none is supplied, so the backend still rejects it", async () => {
    backendStatus = 401;
    backendBody = JSON.stringify({ error: "missing bearer token", code: "unauthorized" });
    const res = await call("?issue_id=issue-1&limit=20&offset=0");
    expect(captured!.headers.authorization ?? captured!.headers.Authorization).toBeUndefined();
    expect(res.status).toBe(401);
    expect(res.headers.get("cache-control")).toBe("no-store");
  });

  it("passes a backend 401 through unchanged for an invalid token", async () => {
    backendStatus = 401;
    backendBody = JSON.stringify({ error: "invalid token", code: "unauthorized" });
    const res = await call("?issue_id=issue-1&limit=20&offset=0", { authorization: "Bearer bogus" });
    expect(captured!.headers.authorization ?? captured!.headers.Authorization).toBe("Bearer bogus");
    expect(res.status).toBe(401);
  });
});
