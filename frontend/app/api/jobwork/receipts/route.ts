import { NextRequest, NextResponse } from "next/server";

const BACKEND = process.env.BACKEND_URL ?? "http://localhost:8080";

export const dynamic = "force-dynamic";
export const maxDuration = 60;

export async function GET(req: NextRequest) {
  const target = new URL("/jobwork/receipts", BACKEND);
  const incoming = req.nextUrl.searchParams;
  for (const k of ["issue_id", "limit", "offset"]) {
    const v = incoming.get(k);
    if (v) target.searchParams.set(k, v);
  }
  try {
    const res = await fetch(target.toString(), { cache: "no-store", signal: AbortSignal.timeout(30000) });
    return new NextResponse(await res.text(), { status: res.status, headers: { "content-type": "application/json", "cache-control": "no-store" } });
  } catch {
    return NextResponse.json({ error: "backend unreachable" }, { status: 502, headers: { "cache-control": "no-store" } });
  }
}
