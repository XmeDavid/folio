import { fetchFxRate, fetchMultipleFxRates } from "./frankfurter";

export type Currency = "USD" | "EUR" | "CHF";

export async function convertAmount(
  amount: number,
  from: Currency,
  to: Currency,
  date: string
): Promise<number> {
  if (from === to) return amount;
  const rate = await fetchFxRate(date, from, to);
  return amount * rate;
}

export async function getCurrentFxRate(
  from: Currency,
  to: Currency
): Promise<number> {
  if (from === to) return 1;
  const today = new Date().toISOString().split("T")[0];
  return fetchFxRate(today, from, to);
}

/**
 * Prefetch a date range of FX rates into the DB cache and return
 * a lookup map keyed by date string. Non-trading days are filled
 * forward with the last known rate.
 */
export async function getFxRateRange(
  dateFrom: string,
  dateTo: string,
  from: Currency,
  to: Currency
): Promise<Map<string, number>> {
  if (from === to) {
    const m = new Map<string, number>();
    let d = new Date(dateFrom);
    const end = new Date(dateTo);
    while (d <= end) {
      m.set(d.toISOString().split("T")[0], 1);
      d.setDate(d.getDate() + 1);
    }
    return m;
  }

  const raw = await fetchMultipleFxRates(dateFrom, dateTo, from, to);
  const filled = new Map<string, number>();
  let d = new Date(dateFrom);
  const end = new Date(dateTo);
  let lastRate: number | null = null;

  while (d <= end) {
    const key = d.toISOString().split("T")[0];
    const rate = raw.get(key);
    if (rate !== undefined) {
      lastRate = rate;
    }
    if (lastRate !== null) {
      filled.set(key, lastRate);
    }
    d.setDate(d.getDate() + 1);
  }

  return filled;
}

export function formatCurrency(amount: number, currency: Currency): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency,
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(amount);
}
