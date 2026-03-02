import { db } from "@/db";
import { transactions } from "@/db/schema";
import { asc, eq } from "drizzle-orm";
import { normalizeTicker, DELISTED_ZERO_CLOSE_CUTOFF } from "./ticker-aliases";
import { BUY_TYPES, SELL_TYPES, ACCOUNT_FEE_TYPES } from "./positions";
import { getFxRateRange, type Currency } from "../fx/convert";
import { fetchHistoricalRange } from "../prices/yahoo";

export interface TimeSeriesPoint {
  date: string;
  portfolioValue: number;
  costBasis: number;
  realizedPnL: number;
  unrealizedPnL: number;
  dividends: number;
  fees: number;
  totalReturn: number;
  priceOnlyReturn: number;
}

export interface TickerSeriesPoint {
  date: string;
  quantity: number;
  marketValue: number;
  costBasis: number;
  unrealizedPnL: number;
  realizedPnL: number;
  dividends: number;
  fees: number;
  totalReturn: number;
  priceOnlyReturn: number;
}

interface RunningPosition {
  quantity: number;
  costBasis: number;
  totalInvested: number;
  realizedPnL: number;
  dividends: number;
  fees: number;
  currency: string;
}

const POSITION_TRACKED_TYPES = new Set([
  ...BUY_TYPES,
  ...SELL_TYPES,
  "STOCK SPLIT",
  "DIVIDEND",
  "DIVIDEND TAX (CORRECTION)",
  ...ACCOUNT_FEE_TYPES,
]);

function isPositionTrackedType(type: string): boolean {
  return POSITION_TRACKED_TYPES.has(type);
}

function addDays(dateStr: string, n: number): string {
  const d = new Date(dateStr);
  d.setDate(d.getDate() + n);
  return d.toISOString().split("T")[0];
}

function getAllDatesBetween(from: string, to: string): string[] {
  const dates: string[] = [];
  let d = new Date(from);
  const end = new Date(to);
  while (d <= end) {
    dates.push(d.toISOString().split("T")[0]);
    d.setDate(d.getDate() + 1);
  }
  return dates;
}

/**
 * Fetch historical close prices (auto-populates DB cache when needed).
 * Returns map: ticker -> date -> price.
 * Missing prices are forward-filled from last known price.
 */
async function loadPriceGrid(
  tickers: string[],
  from: string,
  to: string,
  preferredCurrencies?: Map<string, string>
): Promise<Map<string, Map<string, number>>> {
  const uniqueTickers = [...new Set(tickers)];
  const grid = new Map<string, Map<string, number>>();
  for (const ticker of uniqueTickers) {
    grid.set(ticker, new Map());
  }

  // Keep request concurrency modest to avoid rate limiting.
  const BATCH = 6;
  for (let i = 0; i < uniqueTickers.length; i += BATCH) {
    const batch = uniqueTickers.slice(i, i + BATCH);
    const results = await Promise.all(
      batch.map(async (ticker) => ({
        ticker,
        prices: await fetchHistoricalRange(
          ticker,
          from,
          to,
          preferredCurrencies?.get(ticker)
        ),
      }))
    );

    for (const { ticker, prices } of results) {
      grid.set(ticker, prices);
    }
  }

  const dates = getAllDatesBetween(from, to);
  for (const ticker of uniqueTickers) {
    const priceMap = grid.get(ticker)!;
    let last: number | null = null;

    // Backfill initial dates with first known in range.
    const firstKnownDate = dates.find((d) => priceMap.has(d));
    if (firstKnownDate) {
      const firstVal = priceMap.get(firstKnownDate)!;
      for (const d of dates) {
        if (d < firstKnownDate) {
          priceMap.set(d, firstVal);
        } else {
          break;
        }
      }
    }

    for (const date of dates) {
      const p = priceMap.get(date);
      if (p !== undefined) {
        last = p;
      } else if (last !== null) {
        priceMap.set(date, last);
      }
    }
  }

  return grid;
}

function applyTransactionToPosition(
  pos: RunningPosition,
  tx: (typeof transactions.$inferSelect)
): void {
  const qty = parseFloat(tx.quantity || "0");
  const total = parseFloat(tx.totalAmount);
  const commission = parseFloat(tx.commission || "0");
  pos.fees += commission;

  if (BUY_TYPES.includes(tx.type)) {
    const prev = pos.quantity;
    pos.quantity += qty;
    pos.costBasis =
      prev + qty > 0
        ? (pos.costBasis * prev + Math.abs(total)) / (prev + qty)
        : 0;
    pos.totalInvested += Math.abs(total);
  } else if (SELL_TYPES.includes(tx.type)) {
    const proceeds = Math.abs(total);
    pos.realizedPnL += proceeds - pos.costBasis * qty;
    pos.quantity -= qty;
    if (pos.quantity <= 0.00001) {
      pos.quantity = 0;
      pos.costBasis = 0;
    }
  } else if (tx.type === "STOCK SPLIT") {
    pos.quantity += qty;
    if (pos.quantity > 0 && pos.totalInvested > 0) {
      pos.costBasis = pos.totalInvested / pos.quantity;
    }
  } else if (
    tx.type === "DIVIDEND" ||
    tx.type === "DIVIDEND TAX (CORRECTION)"
  ) {
    pos.dividends += total;
  } else if (tx.type === "CUSTODY FEE") {
    pos.fees += Math.abs(total);
  }
}

export async function getPortfolioTimeSeries(opts: {
  from?: string;
  to?: string;
  currency: Currency;
  accountId?: string;
}): Promise<TimeSeriesPoint[]> {
  const query = db.select().from(transactions);
  if (opts.accountId) query.where(eq(transactions.accountId, opts.accountId));
  const allTxns = await query.orderBy(asc(transactions.date));

  if (allTxns.length === 0) return [];

  const firstDate = allTxns[0].date.toISOString().split("T")[0];
  const from = opts.from || firstDate;
  const to = opts.to || new Date().toISOString().split("T")[0];

  const txUntilTo = allTxns.filter(
    (tx) => tx.date.toISOString().split("T")[0] <= to
  );

  const positions = new Map<string, RunningPosition>();
  const txByDate = new Map<string, (typeof allTxns)>();
  let accountFees = 0;
  let accountFeeCurrency = "USD";

  for (const tx of txUntilTo) {
    const d = tx.date.toISOString().split("T")[0];
    if (!tx.ticker && ACCOUNT_FEE_TYPES.includes(tx.type)) {
      accountFeeCurrency = tx.currency;
      if (d < from) {
        accountFees += Math.abs(parseFloat(tx.totalAmount));
      } else {
        if (!txByDate.has(d)) txByDate.set(d, []);
        txByDate.get(d)!.push(tx);
      }
      continue;
    }
    if (!isPositionTrackedType(tx.type)) continue;
    if (tx.ticker) {
      const nt = normalizeTicker(tx.ticker);
      if (!positions.has(nt)) {
        positions.set(nt, {
          quantity: 0, costBasis: 0, totalInvested: 0,
          realizedPnL: 0, dividends: 0, fees: 0,
          currency: tx.currency,
        });
      }

      if (d < from) {
        applyTransactionToPosition(positions.get(nt)!, tx);
      } else {
        if (!txByDate.has(d)) txByDate.set(d, []);
        txByDate.get(d)!.push(tx);
      }
    }
  }

  const tickers = [...positions.keys()];
  const tickerCurrencies = new Map<string, string>();
  for (const [ticker, pos] of positions) {
    tickerCurrencies.set(ticker, pos.currency);
  }

  const [priceGrid, fxRates] = await Promise.all([
    loadPriceGrid(tickers, from, to, tickerCurrencies),
    getFxRateRange(from, to, "USD", opts.currency),
  ]);

  let eurFx: Map<string, number> | undefined;
  const hasEur = [...positions.values()].some((p) => p.currency === "EUR") || accountFeeCurrency === "EUR";
  if (hasEur && opts.currency !== "EUR") {
    eurFx = await getFxRateRange(from, to, "EUR", opts.currency);
  }

  const dates = getAllDatesBetween(from, to);
  const points: TimeSeriesPoint[] = [];

  for (const date of dates) {
    const dayTxns = txByDate.get(date);
    if (dayTxns) {
      for (const tx of dayTxns) {
        if (!tx.ticker && ACCOUNT_FEE_TYPES.includes(tx.type)) {
          accountFees += Math.abs(parseFloat(tx.totalAmount));
          continue;
        }
        if (!tx.ticker) continue;
        if (!isPositionTrackedType(tx.type)) continue;
        const nt = normalizeTicker(tx.ticker);
        const pos = positions.get(nt)!;
        applyTransactionToPosition(pos, tx);
      }
    }

    for (const [ticker, pos] of positions) {
      const cutoff = DELISTED_ZERO_CLOSE_CUTOFF[ticker];
      if (cutoff && date >= cutoff && pos.quantity > 0.00001) {
        pos.realizedPnL -= pos.costBasis * pos.quantity;
        pos.quantity = 0;
        pos.costBasis = 0;
      }
    }

    const acctFeeFx = accountFeeCurrency === "EUR"
      ? (eurFx?.get(date) ?? 1)
      : accountFeeCurrency === "USD"
        ? (fxRates.get(date) ?? 1)
        : 1;

    let portfolioValue = 0;
    let costBasisTotal = 0;
    let realizedTotal = 0;
    let dividendsTotal = 0;
    let feesTotal = accountFees * acctFeeFx;

    for (const [ticker, pos] of positions) {
      if (pos.quantity <= 0.00001) {
        const fx = pos.currency === "EUR"
          ? (eurFx?.get(date) ?? 1)
          : pos.currency === "USD"
            ? (fxRates.get(date) ?? 1)
            : 1;
        realizedTotal += pos.realizedPnL * fx;
        dividendsTotal += pos.dividends * fx;
        feesTotal += pos.fees * fx;
        continue;
      }

      const price = priceGrid.get(ticker)?.get(date);
      const fx = pos.currency === "EUR"
        ? (eurFx?.get(date) ?? 1)
        : pos.currency === "USD"
          ? (fxRates.get(date) ?? 1)
          : 1;

      const mv = (price ?? pos.costBasis) * pos.quantity * fx;
      const cb = pos.costBasis * pos.quantity * fx;

      portfolioValue += mv;
      costBasisTotal += cb;
      realizedTotal += pos.realizedPnL * fx;
      dividendsTotal += pos.dividends * fx;
      feesTotal += pos.fees * fx;
    }

    const unrealizedPnL = portfolioValue - costBasisTotal;
    const totalReturn = unrealizedPnL + realizedTotal + dividendsTotal - feesTotal;
    const priceOnlyReturn = unrealizedPnL + realizedTotal;

    points.push({
      date,
      portfolioValue: Math.round(portfolioValue * 100) / 100,
      costBasis: Math.round(costBasisTotal * 100) / 100,
      realizedPnL: Math.round(realizedTotal * 100) / 100,
      unrealizedPnL: Math.round(unrealizedPnL * 100) / 100,
      dividends: Math.round(dividendsTotal * 100) / 100,
      fees: Math.round(feesTotal * 100) / 100,
      totalReturn: Math.round(totalReturn * 100) / 100,
      priceOnlyReturn: Math.round(priceOnlyReturn * 100) / 100,
    });
  }

  return points;
}

export async function getTickerTimeSeries(opts: {
  ticker: string;
  from?: string;
  to?: string;
  currency: Currency;
  accountId?: string;
}): Promise<TickerSeriesPoint[]> {
  const query = db.select().from(transactions);
  if (opts.accountId) query.where(eq(transactions.accountId, opts.accountId));
  const allTxns = await query.orderBy(asc(transactions.date));

  const canonicalTicker = normalizeTicker(opts.ticker);

  const relevantTxns = allTxns.filter(
    (tx) =>
      tx.ticker &&
      isPositionTrackedType(tx.type) &&
      normalizeTicker(tx.ticker) === canonicalTicker
  );
  if (relevantTxns.length === 0) return [];

  const firstDate = relevantTxns[0].date.toISOString().split("T")[0];
  const from = opts.from || firstDate;
  const to = opts.to || new Date().toISOString().split("T")[0];

  const txByDate = new Map<string, (typeof relevantTxns)>();
  let tickerCurrency = "USD";

  for (const tx of relevantTxns) {
    const d = tx.date.toISOString().split("T")[0];
    tickerCurrency = tx.currency;
    if (d >= from && d <= to) {
      if (!txByDate.has(d)) txByDate.set(d, []);
      txByDate.get(d)!.push(tx);
    }
  }

  const [priceGrid, fxRates] = await Promise.all([
    loadPriceGrid(
      [canonicalTicker],
      from,
      to,
      new Map([[canonicalTicker, tickerCurrency]])
    ),
    getFxRateRange(from, to, tickerCurrency as Currency, opts.currency),
  ]);

  const dates = getAllDatesBetween(from, to);
  const points: TickerSeriesPoint[] = [];
  const pos: RunningPosition = {
    quantity: 0, costBasis: 0, totalInvested: 0,
    realizedPnL: 0, dividends: 0, fees: 0,
    currency: tickerCurrency,
  };

  // Build opening state before chart range.
  for (const tx of relevantTxns) {
    const d = tx.date.toISOString().split("T")[0];
    if (d < from) {
      applyTransactionToPosition(pos, tx);
    }
  }

  for (const date of dates) {
    const dayTxns = txByDate.get(date);
    if (dayTxns) {
      for (const tx of dayTxns) {
        applyTransactionToPosition(pos, tx);
      }
    }

    const cutoff = DELISTED_ZERO_CLOSE_CUTOFF[canonicalTicker];
    if (cutoff && date >= cutoff && pos.quantity > 0.00001) {
      pos.realizedPnL -= pos.costBasis * pos.quantity;
      pos.quantity = 0;
      pos.costBasis = 0;
    }

    const price = priceGrid.get(canonicalTicker)?.get(date);
    const fx = fxRates.get(date) ?? 1;

    const mv = pos.quantity > 0.00001
      ? (price ?? pos.costBasis) * pos.quantity * fx
      : 0;
    const cb = pos.costBasis * pos.quantity * fx;
    const unrealized = mv - cb;
    const totalReturn = unrealized + pos.realizedPnL * fx + pos.dividends * fx - pos.fees * fx;
    const priceOnly = unrealized + pos.realizedPnL * fx;

    points.push({
      date,
      quantity: Math.round(pos.quantity * 1e8) / 1e8,
      marketValue: Math.round(mv * 100) / 100,
      costBasis: Math.round(cb * 100) / 100,
      unrealizedPnL: Math.round(unrealized * 100) / 100,
      realizedPnL: Math.round(pos.realizedPnL * fx * 100) / 100,
      dividends: Math.round(pos.dividends * fx * 100) / 100,
      fees: Math.round(pos.fees * fx * 100) / 100,
      totalReturn: Math.round(totalReturn * 100) / 100,
      priceOnlyReturn: Math.round(priceOnly * 100) / 100,
    });
  }

  return points;
}
