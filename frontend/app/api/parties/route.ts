import { NextRequest, NextResponse } from "next/server";

const BACKEND = process.env.BACKEND_URL ?? "http://localhost:8080";

export const dynamic = "force-dynamic";
export const maxDuration = 60;

function forward(res: Response, body: string) {
  return new NextResponse(body, { status: res.status, headers: { "content-type": "application/json" } });
}

export async function GET(req: NextRequest) {
  const auth = req.headers.get("authorization") ?? "";
  try {
    const res = await fetch(new URL("/parties", BACKEND).toString(), {
      headers: auth ? { authorization: auth } : {},
      cache: "no-store",
      signal: AbortSignal.timeout(30000),
    });
    return forward(res, await res.text());
  } catch {
    return NextResponse.json({ error: "backend unreachable" }, { status: 502 });
  }
}

export async function POST(req: NextRequest) {
  const auth = req.headers.get("authorization") ?? "";
  const body = await req.text();
  try {
    const res = await fetch(new URL("/parties", BACKEND).toString(), {
      method: "POST",
      headers: { "content-type": "application/json", ...(auth ? { authorization: auth } : {}) },
      body,
      cache: "no-store",
      signal: AbortSignal.timeout(30000),
    });
    return forward(res, await res.text());
  } catch {
    return NextResponse.json({ error: "backend unreachable" }, { status: 502 });
  }
}
