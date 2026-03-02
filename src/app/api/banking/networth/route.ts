import { NextRequest, NextResponse } from "next/server";
import { getNetWorthTimeSeries, getCurrentBalances } from "@/lib/banking/networth";
import type { Currency } from "@/lib/fx/convert";

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const from = url.searchParams.get("from") ?? undefined;
  const to = url.searchParams.get("to") ?? undefined;
  const currency = (url.searchParams.get("currency") || "CHF") as Currency;
  const view = url.searchParams.get("view") || "timeseries";

  if (view === "balances") {
    const balances = await getCurrentBalances(currency);
    return NextResponse.json({ balances });
  }

  const series = await getNetWorthTimeSeries({ from, to, currency });
  return NextResponse.json({ series });
}
