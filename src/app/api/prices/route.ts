import { NextRequest, NextResponse } from "next/server";
import { fetchCurrentPrice } from "@/lib/prices/yahoo";

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const ticker = url.searchParams.get("ticker");

  if (!ticker) {
    return NextResponse.json({ error: "ticker param required" }, { status: 400 });
  }

  const result = await fetchCurrentPrice(ticker);
  if (!result) {
    return NextResponse.json({ error: `No price found for ${ticker}` }, { status: 404 });
  }

  return NextResponse.json({ ticker, ...result });
}
