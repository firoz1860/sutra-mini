import { NextRequest, NextResponse } from "next/server";

const BACKEND = process.env.BACKEND_URL ?? "http://localhost:8080";

export const dynamic = "force-dynamic";
export const maxDuration = 60;

export async function PATCH(req: NextRequest, { params }: { params: { id: string } }) {
  const auth = req.headers.get("authorization") ?? "";
  const body = await req.text();
  const target = new URL(`/parties/${encodeURIComponent(params.id)}`, BACKEND);
  try {
    const res = await fetch(target.toString(), {
      method: "PATCH",
      headers: { "content-type": "application/json", ...(auth ? { authorization: auth } : {}) },
      body,
      cache: "no-store",
      signal: AbortSignal.timeout(30000),
    });
    return new NextResponse(await res.text(), { status: res.status, headers: { "content-type": "application/json" } });
  } catch {
    return NextResponse.json({ error: "backend unreachable" }, { status: 502 });
  }
}
