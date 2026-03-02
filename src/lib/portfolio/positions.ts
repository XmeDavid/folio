import { db } from "@/db";
import { transactions, accounts } from "@/db/schema";
import { asc, eq } from "drizzle-orm";
import { normalizeTicker, DELISTED_ZERO_CLOSE_CUTOFF } from "./ticker-aliases";

export const BUY_TYPES = ["BUY - MARKET", "BUY - LIMIT", "BUY", "MERGER - STOCK"];
export const SELL_TYPES = ["SELL - MARKET", "SELL - LIMIT", "SELL", "POSITION CLOSURE"];
export const ACCOUNT_FEE_TYPES = ["CUSTODY FEE", "ROBO MANAGEMENT FEE"];

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

export interface Position {
  ticker: string;
  quantity: number;
  avgCostBasis: number;
  totalInvested: number;
  totalSold: number;
  realizedPnL: number;
  currency: string;
  accountId: string;
  broker: string;
  lastTransactionDate: string;
  dividendsReceived: number;
  commissionsTotal: number;
}

export async function computePositions(opts?: { accountId?: string }): Promise<Position[]> {
  const query = db.select().from(transactions);
  if (opts?.accountId) {
    query.where(eq(transactions.accountId, opts.accountId));
  }
  const allTxns = await query.orderBy(asc(transactions.date));

  const posMap = new Map<
    string,
    {
      ticker: string;
      quantity: number;
      costBasis: number;
      totalInvested: number;
      totalSold: number;
      realizedPnL: number;
      currency: string;
      accountId: string;
      lastDate: string;
      dividends: number;
      commissions: number;
    }
  >();

  let accountLevelFees = 0;
  let accountFeeCurrency = "USD";

  for (const tx of allTxns) {
    if (!tx.ticker && ACCOUNT_FEE_TYPES.includes(tx.type)) {
      accountLevelFees += Math.abs(parseFloat(tx.totalAmount));
      accountFeeCurrency = tx.currency;
      continue;
    }
    if (!tx.ticker) continue;
    if (!isPositionTrackedType(tx.type)) continue;

    const normalizedTicker = normalizeTicker(tx.ticker);
    const key = `${tx.accountId}:${normalizedTicker}`;
    if (!posMap.has(key)) {
      posMap.set(key, {
        ticker: normalizedTicker,
        quantity: 0,
        costBasis: 0,
        totalInvested: 0,
        totalSold: 0,
        realizedPnL: 0,
        currency: tx.currency,
        accountId: tx.accountId,
        lastDate: "",
        dividends: 0,
        commissions: 0,
      });
    }
    const pos = posMap.get(key)!;
    pos.lastDate = tx.date.toISOString().split("T")[0];

    const qty = parseFloat(tx.quantity || "0");
    const total = parseFloat(tx.totalAmount);
    const commission = parseFloat(tx.commission || "0");
    pos.commissions += commission;

    if (BUY_TYPES.includes(tx.type)) {
      const prevQty = pos.quantity;
      const prevCost = pos.costBasis;
      pos.quantity += qty;
      pos.costBasis = prevQty + qty > 0
        ? (prevCost * prevQty + Math.abs(total)) / (prevQty + qty)
        : 0;
      pos.totalInvested += Math.abs(total);
    } else if (SELL_TYPES.includes(tx.type)) {
      const proceeds = Math.abs(total);
      const costOfSold = pos.costBasis * qty;
      pos.realizedPnL += proceeds - costOfSold;
      pos.quantity -= qty;
      pos.totalSold += proceeds;
      if (pos.quantity <= 0.00001) {
        pos.quantity = 0;
        pos.costBasis = 0;
      }
    } else if (tx.type === "STOCK SPLIT") {
      // qty can be negative (old shares removed) or positive (new shares added)
      pos.quantity += qty;
      if (pos.quantity > 0 && pos.costBasis > 0) {
        pos.costBasis = pos.totalInvested / pos.quantity;
      }
    } else if (tx.type === "DIVIDEND" || tx.type === "DIVIDEND TAX (CORRECTION)") {
      pos.dividends += total;
    }
  }

  for (const pos of posMap.values()) {
    const cutoff = DELISTED_ZERO_CLOSE_CUTOFF[pos.ticker];
    if (!cutoff || pos.quantity <= 0.00001) continue;
    if (pos.lastDate <= cutoff) {
      // Force-close remaining delisted quantity at zero proceeds.
      pos.realizedPnL -= pos.costBasis * pos.quantity;
      pos.quantity = 0;
      pos.costBasis = 0;
    }
  }

  const accountLookup = new Map<string, { name: string; broker: string }>();
  const accts = await db
    .select({ id: accounts.id, name: accounts.name, broker: accounts.broker })
    .from(accounts);
  for (const row of accts) {
    accountLookup.set(row.id, { name: row.name, broker: row.broker });
  }

  return Array.from(posMap.values()).map((p) => {
    const acct = accountLookup.get(p.accountId);
    return {
      ticker: p.ticker,
      quantity: Math.round(p.quantity * 1e8) / 1e8,
      avgCostBasis: Math.round(p.costBasis * 100) / 100,
      totalInvested: Math.round(p.totalInvested * 100) / 100,
      totalSold: Math.round(p.totalSold * 100) / 100,
      realizedPnL: Math.round(p.realizedPnL * 100) / 100,
      currency: p.currency,
      accountId: p.accountId,
      broker: acct?.broker || "Unknown",
      lastTransactionDate: p.lastDate,
      dividendsReceived: Math.round(p.dividends * 100) / 100,
      commissionsTotal: Math.round(p.commissions * 100) / 100,
    };
  });
}

export interface AccountFees {
  amount: number;
  currency: string;
}

export async function getAccountLevelFees(opts?: { accountId?: string }): Promise<AccountFees[]> {
  const query = db.select().from(transactions);
  if (opts?.accountId) query.where(eq(transactions.accountId, opts.accountId));
  const allTxns = await query.orderBy(asc(transactions.date));

  const feesByAccount = new Map<string, { amount: number; currency: string }>();
  for (const tx of allTxns) {
    if (tx.ticker || !ACCOUNT_FEE_TYPES.includes(tx.type)) continue;
    const key = tx.accountId;
    const entry = feesByAccount.get(key) ?? { amount: 0, currency: tx.currency };
    entry.amount += Math.abs(parseFloat(tx.totalAmount));
    feesByAccount.set(key, entry);
  }
  return Array.from(feesByAccount.values());
}

export async function getActivePositions(opts?: { accountId?: string }): Promise<Position[]> {
  const all = await computePositions(opts);
  return all.filter((p) => p.quantity > 0.00001);
}

export async function getClosedPositions(opts?: { accountId?: string }): Promise<Position[]> {
  const all = await computePositions(opts);
  return all.filter((p) => p.quantity <= 0.00001);
}
