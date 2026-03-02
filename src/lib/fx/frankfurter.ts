import { db } from "@/db";
import { fxRates } from "@/db/schema";
import { and, eq } from "drizzle-orm";

const BASE_URL = "https://api.frankfurter.app";

interface FrankfurterResponse {
  amount: number;
  base: string;
  date: string;
  rates: Record<string, number>;
}

export async function fetchFxRate(
  date: string,
  base: string,
  target: string
): Promise<number> {
  const cached = await db
    .select()
    .from(fxRates)
    .where(
      and(
        eq(fxRates.date, date),
        eq(fxRates.base, base),
        eq(fxRates.target, target)
      )
    )
    .limit(1);

  if (cached.length > 0) {
    return parseFloat(cached[0].rate);
  }

  const url = `${BASE_URL}/${date}?from=${base}&to=${target}`;
  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`FX fetch failed: ${res.status} ${url}`);
  }

  const data: FrankfurterResponse = await res.json();
  const rate = data.rates[target];
  if (!rate) throw new Error(`No rate for ${target} on ${date}`);

  await db
    .insert(fxRates)
    .values({ date: data.date, base, target, rate: rate.toString() })
    .onConflictDoNothing();

  return rate;
}

export async function fetchMultipleFxRates(
  dateFrom: string,
  dateTo: string,
  base: string,
  target: string
): Promise<Map<string, number>> {
  const url = `${BASE_URL}/${dateFrom}..${dateTo}?from=${base}&to=${target}`;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`FX range fetch failed: ${res.status}`);

  const data = await res.json();
  const ratesMap = new Map<string, number>();

  const entries = data.rates as Record<string, Record<string, number>>;
  const toInsert: { date: string; base: string; target: string; rate: string }[] = [];

  for (const [dateStr, rates] of Object.entries(entries)) {
    const rate = rates[target];
    if (rate) {
      ratesMap.set(dateStr, rate);
      toInsert.push({ date: dateStr, base, target, rate: rate.toString() });
    }
  }

  if (toInsert.length > 0) {
    const BATCH = 100;
    for (let i = 0; i < toInsert.length; i += BATCH) {
      await db
        .insert(fxRates)
        .values(toInsert.slice(i, i + BATCH))
        .onConflictDoNothing();
    }
  }

  return ratesMap;
}
