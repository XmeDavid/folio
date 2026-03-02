import { NextRequest, NextResponse } from "next/server";
import { getPortfolioSummary } from "@/lib/portfolio/performance";
import { getActivePositions, getClosedPositions, computePositions } from "@/lib/portfolio/positions";
import { getPortfolioTimeSeries, getTickerTimeSeries } from "@/lib/portfolio/timeseries";
import type { Currency } from "@/lib/fx/convert";

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const currency = (url.searchParams.get("currency") || "CHF") as Currency;
  const view = url.searchParams.get("view") || "summary";
  const accountId = url.searchParams.get("accountId") || undefined;

  try {
    if (view === "positions") {
      const status = url.searchParams.get("status") || "open";
      if (status === "closed") {
        return NextResponse.json(await getClosedPositions({ accountId }));
      }
      if (status === "all") {
        return NextResponse.json(await computePositions({ accountId }));
      }
      return NextResponse.json(await getActivePositions({ accountId }));
    }

    if (view === "closed") {
      return NextResponse.json(await getClosedPositions({ accountId }));
    }

    if (view === "timeseries") {
      const from = url.searchParams.get("from") || undefined;
      const to = url.searchParams.get("to") || undefined;
      const series = await getPortfolioTimeSeries({ from, to, currency, accountId });
      return NextResponse.json(series);
    }

    if (view === "ticker") {
      const ticker = url.searchParams.get("ticker");
      if (!ticker) {
        return NextResponse.json(
          { error: "ticker param required for view=ticker" },
          { status: 400 }
        );
      }
      const from = url.searchParams.get("from") || undefined;
      const to = url.searchParams.get("to") || undefined;
      const series = await getTickerTimeSeries({ ticker, from, to, currency, accountId });
      return NextResponse.json(series);
    }

    const summary = await getPortfolioSummary(currency, accountId);
    return NextResponse.json(summary);
  } catch (err) {
    console.error("Portfolio API error:", err);
    return NextResponse.json(
      { error: String(err) },
      { status: 500 }
    );
  }
}
