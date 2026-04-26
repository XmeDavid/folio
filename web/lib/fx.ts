import { multiplyDecimalStrings } from "@/lib/decimal";

const FRANKFURTER_API = "https://api.frankfurter.dev/v2";
const COINBASE_API = "https://api.coinbase.com/v2";

export type FxRate = {
  from: string;
  to: string;
  rate: string;
  date: string;
  provider: "frankfurter" | "coinbase";
};

type RateResponse = {
  date?: string;
  rate?: number | string;
};

type CoinbaseExchangeRatesResponse = {
  data?: {
    currency?: string;
    rates?: Record<string, string>;
  };
};

export async function fetchLatestFxRates(
  currencies: string[],
  baseCurrency: string
): Promise<Record<string, FxRate>> {
  const target = baseCurrency.toUpperCase();
  const sources = [...new Set(currencies.map((c) => c.toUpperCase()))].filter(
    (c) => c && c !== target
  );

  const results = await Promise.allSettled(
    sources.map(async (source) => {
      const rate =
        (await fetchFrankfurterRate(source, target)) ??
        (await fetchCoinbaseRate(source, target));
      return rate ? ([source, rate] as const) : null;
    })
  );

  return Object.fromEntries(
    results
      .map((result) => (result.status === "fulfilled" ? result.value : null))
      .filter((entry): entry is readonly [string, FxRate] => Boolean(entry))
  );
}

export function convertAmount(
  amount: string,
  currency: string,
  baseCurrency: string,
  rates: Record<string, FxRate>
): string | null {
  const from = currency.toUpperCase();
  const to = baseCurrency.toUpperCase();
  if (from === to) return amount;
  const rate = rates[from]?.rate;
  return rate ? multiplyDecimalStrings(amount, rate) : null;
}

async function fetchFrankfurterRate(
  source: string,
  target: string
): Promise<FxRate | null> {
  const url = `${FRANKFURTER_API}/rate/${encodeURIComponent(
    source
  )}/${encodeURIComponent(target)}`;
  try {
    const res = await fetch(url);
    if (!res.ok) return null;
    const body = (await res.json()) as RateResponse;
    if (body.rate == null || !body.date) return null;
    return {
      from: source,
      to: target,
      rate: String(body.rate),
      date: body.date,
      provider: "frankfurter",
    };
  } catch {
    return null;
  }
}

async function fetchCoinbaseRate(
  source: string,
  target: string
): Promise<FxRate | null> {
  const url = `${COINBASE_API}/exchange-rates?currency=${encodeURIComponent(
    source
  )}`;
  try {
    const res = await fetch(url);
    if (!res.ok) return null;
    const body = (await res.json()) as CoinbaseExchangeRatesResponse;
    const rate = body.data?.rates?.[target];
    if (!rate) return null;
    return {
      from: source,
      to: target,
      rate,
      date: new Date().toISOString().slice(0, 10),
      provider: "coinbase",
    };
  } catch {
    return null;
  }
}
