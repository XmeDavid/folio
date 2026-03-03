import { db } from "@/db";
import { bankingTransactions, accounts } from "@/db/schema";
import { and, eq, gte, lte, ne, sql, asc } from "drizzle-orm";
import { getFxRateRange, type Currency } from "@/lib/fx/convert";

export interface SpendingBreakdown {
  totalSpending: number;
  totalIncome: number;
  net: number;
  byCategory: { category: string; total: number; count: number }[];
  byMonth: { month: string; spending: number; income: number; net: number }[];
  topMerchants: { merchant: string; total: number; count: number }[];
}

export async function getSpendingBreakdown(opts: {
  from?: string;
  to?: string;
  accountId?: string;
  excludeTransfers?: boolean;
}): Promise<SpendingBreakdown> {
  const conditions = [];
  // Only completed transactions
  conditions.push(eq(bankingTransactions.status, "completed"));

  if (opts.from) conditions.push(gte(bankingTransactions.date, new Date(opts.from)));
  if (opts.to) conditions.push(lte(bankingTransactions.date, new Date(opts.to)));
  if (opts.accountId) conditions.push(eq(bankingTransactions.accountId, opts.accountId));
  if (opts.excludeTransfers !== false) {
    // Exclude internal transfers by default
    conditions.push(
      sql`(${bankingTransactions.transferType} IS NULL OR ${bankingTransactions.transferType} NOT IN ('internal', 'fx'))`
    );
  }

  const where = and(...conditions);

  const rows = await db
    .select({
      amount: bankingTransactions.amount,
      currency: bankingTransactions.currency,
      category: bankingTransactions.category,
      merchant: bankingTransactions.merchant,
      date: bankingTransactions.date,
    })
    .from(bankingTransactions)
    .where(where)
    .orderBy(asc(bankingTransactions.date));

  // Collect unique non-CHF currencies and date range for FX prefetch
  const displayCurrency: Currency = "CHF";
  const foreignCurrencies = new Set<string>();
  let minDate: string | null = null;
  let maxDate: string | null = null;

  for (const row of rows) {
    const cur = row.currency ?? "CHF";
    if (cur !== displayCurrency) foreignCurrencies.add(cur);
    const dateStr = row.date.toISOString().split("T")[0];
    if (!minDate || dateStr < minDate) minDate = dateStr;
    if (!maxDate || dateStr > maxDate) maxDate = dateStr;
  }

  // Prefetch FX rates for each foreign currency (in parallel)
  const fxMaps = new Map<string, Map<string, number>>();
  if (minDate && maxDate && foreignCurrencies.size > 0) {
    await Promise.all(
      [...foreignCurrencies].map(async (cur) => {
        try {
          const fxMap = await getFxRateRange(minDate!, maxDate!, cur as Currency, displayCurrency);
          fxMaps.set(cur, fxMap);
        } catch {
          // If FX rate unavailable, fallback to 1 (will be raw amount)
        }
      })
    );
  }

  let totalSpending = 0;
  let totalIncome = 0;
  const categoryMap = new Map<string, { total: number; count: number }>();
  const merchantMap = new Map<string, { total: number; count: number }>();
  const monthMap = new Map<string, { spending: number; income: number }>();

  for (const row of rows) {
    const rawAmount = parseFloat(row.amount);
    const cur = row.currency ?? "CHF";
    const dateStr = row.date.toISOString().split("T")[0];

    // Convert to display currency
    let fx = 1;
    if (cur !== displayCurrency) {
      fx = fxMaps.get(cur)?.get(dateStr) ?? 1;
    }
    const amount = rawAmount * fx;

    if (amount < 0) {
      totalSpending += amount;
    } else {
      totalIncome += amount;
    }

    // Category breakdown
    const cat = row.category || "Uncategorized";
    const catEntry = categoryMap.get(cat) || { total: 0, count: 0 };
    catEntry.total += amount;
    catEntry.count += 1;
    categoryMap.set(cat, catEntry);

    // Merchant breakdown (only spending)
    if (amount < 0 && row.merchant) {
      const merchEntry = merchantMap.get(row.merchant) || { total: 0, count: 0 };
      merchEntry.total += amount;
      merchEntry.count += 1;
      merchantMap.set(row.merchant, merchEntry);
    }

    // Monthly breakdown
    const month = row.date.toISOString().slice(0, 7); // YYYY-MM
    const monthEntry = monthMap.get(month) || { spending: 0, income: 0 };
    if (amount < 0) {
      monthEntry.spending += amount;
    } else {
      monthEntry.income += amount;
    }
    monthMap.set(month, monthEntry);
  }

  const byCategory = [...categoryMap.entries()]
    .map(([category, { total, count }]) => ({
      category,
      total: Math.round(total * 100) / 100,
      count,
    }))
    .sort((a, b) => a.total - b.total); // most negative first

  const topMerchants = [...merchantMap.entries()]
    .map(([merchant, { total, count }]) => ({
      merchant,
      total: Math.round(total * 100) / 100,
      count,
    }))
    .sort((a, b) => a.total - b.total) // most negative first
    .slice(0, 20);

  const byMonth = [...monthMap.entries()]
    .map(([month, { spending, income }]) => ({
      month,
      spending: Math.round(spending * 100) / 100,
      income: Math.round(income * 100) / 100,
      net: Math.round((income + spending) * 100) / 100,
    }))
    .sort((a, b) => a.month.localeCompare(b.month));

  return {
    totalSpending: Math.round(totalSpending * 100) / 100,
    totalIncome: Math.round(totalIncome * 100) / 100,
    net: Math.round((totalIncome + totalSpending) * 100) / 100,
    byCategory,
    byMonth,
    topMerchants,
  };
}
