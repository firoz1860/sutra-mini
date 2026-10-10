import { NextRequest, NextResponse } from "next/server";

const BACKEND = process.env.BACKEND_URL ?? "http://localhost:8080";

export const dynamic = "force-dynamic";

export async function POST(req: NextRequest) {
  const key = req.headers.get("Idempotency-Key") ?? "";
  const body = await req.text();
  try {
    const res = await fetch(new URL("/jobwork/receipts", BACKEND).toString(), {
      method: "POST",
      headers: { "content-type": "application/json", "Idempotency-Key": key },
      body,
      cache: "no-store",
    });
    const out = await res.text();
    return new NextResponse(out, {
      status: res.status,
      headers: { "content-type": "application/json" },
    });
  } catch {
    return NextResponse.json({ error: "backend unreachable" }, { status: 502 });
  }
}
