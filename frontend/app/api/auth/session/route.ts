import { NextRequest, NextResponse } from "next/server";

const BACKEND = process.env.BACKEND_URL ?? "http://localhost:8080";

export const dynamic = "force-dynamic";
export const maxDuration = 60;

export async function GET(req: NextRequest) {
  const auth = req.headers.get("authorization") ?? "";
  try {
    const res = await fetch(new URL("/auth/session", BACKEND).toString(), {
      headers: auth ? { authorization: auth } : {},
      cache: "no-store",
      signal: AbortSignal.timeout(30000),
    });
    const out = await res.text();
    return new NextResponse(out, { status: res.status, headers: { "content-type": "application/json", "cache-control": "no-store" } });
  } catch {
    return NextResponse.json({ error: "backend unreachable" }, { status: 502, headers: { "cache-control": "no-store" } });
  }
}
