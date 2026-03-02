import { NextRequest, NextResponse } from "next/server";
import { fetchFxRate } from "@/lib/fx/frankfurter";
import { getCurrentFxRate } from "@/lib/fx/convert";

export async function GET(req: NextRequest) {
  const url = new URL(req.url);
  const from = url.searchParams.get("from") || "USD";
  const to = url.searchParams.get("to") || "CHF";
  const date = url.searchParams.get("date");

  try {
    const rate = date
      ? await fetchFxRate(date, from, to)
      : await getCurrentFxRate(from as "USD" | "EUR" | "CHF", to as "USD" | "EUR" | "CHF");

    return NextResponse.json({ from, to, date: date || "latest", rate });
  } catch (err) {
    return NextResponse.json(
      { error: String(err) },
      { status: 500 }
    );
  }
}
