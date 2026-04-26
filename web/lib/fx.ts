import { multiplyDecimalStrings } from "@/lib/decimal";

const FRANKFURTER_API = "https://api.frankfurter.dev/v2";

export type FxRate = {
  from: string;
  to: string;
  rate: string;
  date: string;
};

type RateResponse = {
  date?: string;
  rate?: number | string;
};

export async function fetchLatestFxRates(
  currencies: string[],
  baseCurrency: string
): Promise<Record<string, FxRate>> {
  const target = baseCurrency.toUpperCase();
  const sources = [...new Set(currencies.map((c) => c.toUpperCase()))].filter(
    (c) => c && c !== target
  );
  const entries = await Promise.all(
    sources.map(async (source) => {
      const url = `${FRANKFURTER_API}/rate/${encodeURIComponent(
        source
      )}/${encodeURIComponent(target)}`;
      const res = await fetch(url);
      if (!res.ok) {
        throw new Error(`FX rate unavailable for ${source}/${target}`);
      }
      const body = (await res.json()) as RateResponse;
      if (body.rate == null || !body.date) {
        throw new Error(`FX rate unavailable for ${source}/${target}`);
      }
      return [
        source,
        {
          from: source,
          to: target,
          rate: String(body.rate),
          date: body.date,
        },
      ] as const;
    })
  );

  return Object.fromEntries(entries);
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
