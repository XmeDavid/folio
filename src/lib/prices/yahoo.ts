import YahooFinance from "yahoo-finance2";
import { db } from "@/db";
import { stockPrices } from "@/db/schema";
import { and, asc, eq, gte, lte } from "drizzle-orm";

const yf = new (YahooFinance as unknown as new (opts: Record<string, unknown>) => InstanceType<typeof Object> & {
  quote: (ticker: string) => Promise<Record<string, unknown>>;
  historical: (ticker: string, opts: Record<string, unknown>) => Promise<Array<Record<string, unknown>>>;
  search: (query: string, opts?: Record<string, unknown>) => Promise<Record<string, unknown>>;
})({ suppressNotices: ["yahooSurvey"] });

const quoteSymbolCache = new Map<string, string>();
const historicalSymbolCache = new Map<string, string>();

function normalizeTicker(ticker: string): string {
  return ticker.trim().toUpperCase();
}

function makeCacheKey(ticker: string, preferredCurrency?: string): string {
  return `${normalizeTicker(ticker)}|${(preferredCurrency || "").toUpperCase()}`;
}

function addDays(date: string, days: number): string {
  const d = new Date(date);
  d.setDate(d.getDate() + days);
  return d.toISOString().split("T")[0];
}

async function tryQuote(
  symbol: string
): Promise<{ price: number; currency: string } | null> {
  try {
    const quote = await yf.quote(symbol);
    const price = quote?.regularMarketPrice as number | undefined;
    const currency = quote?.currency as string | undefined;
    if (!price || !Number.isFinite(price)) return null;
    return { price, currency: currency || "USD" };
  } catch {
    return null;
  }
}

async function tryHistorical(
  symbol: string,
  from: string,
  to: string
): Promise<Array<Record<string, unknown>> | null> {
  try {
    const rows = await yf.historical(symbol, {
      period1: new Date(from),
      period2: new Date(addDays(to, 1)),
      interval: "1d",
    });
    if (!rows || rows.length === 0) return null;
    const valid = rows.filter((r) => {
      const close = r.close as number | undefined;
      return !!close && Number.isFinite(close);
    });
    return valid.length > 0 ? valid : null;
  } catch {
    return null;
  }
}

async function getSearchCandidates(
  ticker: string,
  preferredCurrency?: string
): Promise<string[]> {
  try {
    const res = await yf.search(ticker, { quotesCount: 20, newsCount: 0 });
    const quotes = (res.quotes as Array<Record<string, unknown>> | undefined) ?? [];
    const canonical = normalizeTicker(ticker);
    const preferred = preferredCurrency?.toUpperCase();

    const scored: Array<{ symbol: string; score: number }> = [];
    for (const q of quotes) {
      const symbol = String(q.symbol || "").toUpperCase();
      if (!symbol) continue;
      // Keep exact ticker and exchange-suffixed variants (e.g. VGEU.DE).
      if (symbol !== canonical && !symbol.startsWith(`${canonical}.`)) continue;

      const currency = String(q.currency || "").toUpperCase();
      const quoteType = String(q.quoteType || q.typeDisp || "").toUpperCase();
      let score = 0;
      if (symbol === canonical) score += 100;
      if (preferred && currency === preferred) score += 30;
      if (
        quoteType.includes("ETF") ||
        quoteType.includes("EQUITY") ||
        quoteType.includes("FUND")
      ) {
        score += 10;
      }
      // Mild preference for common EUR venues.
      if (symbol.endsWith(".DE")) score += 3;
      if (symbol.endsWith(".AS")) score += 2;
      if (symbol.endsWith(".SW")) score += 1;
      scored.push({ symbol, score });
    }

    scored.sort((a, b) => b.score - a.score);
    const out: string[] = [];
    for (const row of scored) {
      if (!out.includes(row.symbol)) out.push(row.symbol);
    }
    return out;
  } catch {
    return [];
  }
}

const EUR_EXCHANGE_SUFFIXES = [".DE", ".MI", ".MU", ".AS", ".PA", ".L", ".SW", ".BR", ".VI", ".DU", ".F"];

async function tryExchangeSuffixes(
  ticker: string,
  trySym: (sym: string) => Promise<boolean>
): Promise<string | null> {
  for (const suffix of EUR_EXCHANGE_SUFFIXES) {
    const sym = ticker + suffix;
    if (await trySym(sym)) return sym;
  }
  return null;
}

async function resolveQuoteSymbol(
  ticker: string,
  preferredCurrency?: string
): Promise<string> {
  const key = makeCacheKey(ticker, preferredCurrency);
  const cached = quoteSymbolCache.get(key);
  if (cached) return cached;

  const direct = await tryQuote(ticker);
  if (direct) {
    quoteSymbolCache.set(key, ticker);
    return ticker;
  }

  const candidates = await getSearchCandidates(ticker, preferredCurrency);
  for (const candidate of candidates) {
    const q = await tryQuote(candidate);
    if (q) {
      quoteSymbolCache.set(key, candidate);
      return candidate;
    }
  }

  const suffix = await tryExchangeSuffixes(ticker, async (sym) => !!(await tryQuote(sym)));
  if (suffix) {
    quoteSymbolCache.set(key, suffix);
    return suffix;
  }

  return ticker;
}

async function resolveHistoricalSymbol(
  ticker: string,
  from: string,
  to: string,
  preferredCurrency?: string
): Promise<string> {
  const key = makeCacheKey(ticker, preferredCurrency);
  const cached = historicalSymbolCache.get(key);
  if (cached) return cached;

  const quoteResolved = quoteSymbolCache.get(key);
  if (quoteResolved) {
    const rows = await tryHistorical(quoteResolved, from, to);
    if (rows && rows.length > 0) {
      historicalSymbolCache.set(key, quoteResolved);
      return quoteResolved;
    }
  }

  const direct = await tryHistorical(ticker, from, to);
  if (direct && direct.length > 0) {
    historicalSymbolCache.set(key, ticker);
    return ticker;
  }

  const candidates = await getSearchCandidates(ticker, preferredCurrency);
  for (const candidate of candidates) {
    const rows = await tryHistorical(candidate, from, to);
    if (rows && rows.length > 0) {
      historicalSymbolCache.set(key, candidate);
      return candidate;
    }
  }

  const suffix = await tryExchangeSuffixes(ticker, async (sym) => {
    const rows = await tryHistorical(sym, from, to);
    return !!(rows && rows.length > 0);
  });
  if (suffix) {
    historicalSymbolCache.set(key, suffix);
    return suffix;
  }

  return ticker;
}

export async function fetchCurrentPrice(
  ticker: string,
  preferredCurrency?: string
): Promise<{ price: number; currency: string } | null> {
  const symbol = await resolveQuoteSymbol(ticker, preferredCurrency);
  const quote = await tryQuote(symbol);
  if (quote) return quote;

  // one last direct attempt if resolver failed
  const direct = await tryQuote(ticker);
  if (direct) return direct;

  console.warn(`Failed to fetch quote for ${ticker}`);
  return null;
}

export async function fetchHistoricalPrice(
  ticker: string,
  date: string,
  preferredCurrency?: string
): Promise<number | null> {
  const map = await fetchHistoricalRange(
    ticker,
    date,
    addDays(date, 5),
    preferredCurrency
  );
  const direct = map.get(date);
  if (direct !== undefined) return direct;

  const dates = [...map.keys()].sort();
  for (const d of dates) {
    if (d >= date) return map.get(d) ?? null;
  }
  return null;
}

/**
 * Fetch and cache historical range prices for one ticker.
 * Returns date->close map from DB cache (after optional refresh).
 */
export async function fetchHistoricalRange(
  ticker: string,
  from: string,
  to: string,
  preferredCurrency?: string
): Promise<Map<string, number>> {
  let cached = await db
    .select({
      date: stockPrices.date,
      closePrice: stockPrices.closePrice,
    })
    .from(stockPrices)
    .where(
      and(
        eq(stockPrices.ticker, ticker),
        gte(stockPrices.date, from),
        lte(stockPrices.date, to)
      )
    )
    .orderBy(asc(stockPrices.date));

  let needsFetch = cached.length === 0;
  if (!needsFetch && cached.length > 0) {
    const earliestCached = cached[0].date;
    const latestCached = cached[cached.length - 1].date;
    const startThreshold = addDays(from, 7);
    const endThreshold = addDays(to, -7);
    const hasStartCoverage = earliestCached <= startThreshold;
    const hasEndCoverage = latestCached >= endThreshold;
    needsFetch = !hasStartCoverage || !hasEndCoverage;
  }

  if (needsFetch) {
    try {
      const resolvedSymbol = await resolveHistoricalSymbol(
        ticker,
        from,
        to,
        preferredCurrency
      );
      const result = await tryHistorical(resolvedSymbol, from, to);
      if (!result || result.length === 0) {
        throw new Error(`No historical rows for ${ticker}`);
      }

      const toInsert: Array<{
        ticker: string;
        date: string;
        closePrice: string;
        currency: string;
      }> = [];

      for (const row of result) {
        const rowDate = row.date as Date;
        const close = row.close as number | undefined;
        if (!rowDate || !close) continue;
        toInsert.push({
          ticker,
          date: rowDate.toISOString().split("T")[0],
          closePrice: close.toString(),
          currency: "USD",
        });
      }

      const BATCH = 200;
      for (let i = 0; i < toInsert.length; i += BATCH) {
        await db
          .insert(stockPrices)
          .values(toInsert.slice(i, i + BATCH))
          .onConflictDoNothing();
      }
    } catch {
      console.warn(`Failed to fetch historical range for ${ticker}`);
    }

    cached = await db
      .select({
        date: stockPrices.date,
        closePrice: stockPrices.closePrice,
      })
      .from(stockPrices)
      .where(
        and(
          eq(stockPrices.ticker, ticker),
          gte(stockPrices.date, from),
          lte(stockPrices.date, to)
        )
      )
      .orderBy(asc(stockPrices.date));
  }

  const out = new Map<string, number>();
  for (const row of cached) {
    out.set(row.date, parseFloat(row.closePrice));
  }
  return out;
}

export async function fetchBulkCurrentPrices(
  tickers: string[],
  preferredCurrencies?: Map<string, string>
): Promise<Map<string, { price: number; currency: string }>> {
  const results = new Map<string, { price: number; currency: string }>();

  const batchSize = 10;
  for (let i = 0; i < tickers.length; i += batchSize) {
    const batch = tickers.slice(i, i + batchSize);
    const promises = batch.map(async (ticker) => {
      const result = await fetchCurrentPrice(
        ticker,
        preferredCurrencies?.get(ticker)
      );
      if (result) results.set(ticker, result);
    });
    await Promise.all(promises);
  }

  return results;
}
