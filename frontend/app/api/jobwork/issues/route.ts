import { NextRequest, NextResponse } from "next/server";

const BACKEND = process.env.BACKEND_URL ?? "http://localhost:8080";

export const dynamic = "force-dynamic";
export const maxDuration = 60;

export async function POST(req: NextRequest) {
  const auth = req.headers.get("authorization") ?? "";
  const key = req.headers.get("Idempotency-Key") ?? "";
  const body = await req.text();
  try {
    const res = await fetch(new URL("/jobwork/issues", BACKEND).toString(), {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "Idempotency-Key": key,
        ...(auth ? { authorization: auth } : {}),
      },
      body,
      cache: "no-store",
      signal: AbortSignal.timeout(30000),
    });
    return new NextResponse(await res.text(), { status: res.status, headers: { "content-type": "application/json", "cache-control": "no-store" } });
  } catch {
    return NextResponse.json({ error: "backend unreachable" }, { status: 502, headers: { "cache-control": "no-store" } });
  }
}
