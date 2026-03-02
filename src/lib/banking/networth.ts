import { db } from "@/db";
import { bankingTransactions, accounts } from "@/db/schema";
import { eq, asc } from "drizzle-orm";
import { getPortfolioTimeSeries } from "@/lib/portfolio/timeseries";
import { getFxRateRange, type Currency } from "@/lib/fx/convert";

export interface NetWorthPoint {
  date: string;
  total: number;
  accounts: Record<string, number>;
}

export interface AccountBalance {
  accountId: string;
  accountName: string;
  accountType: string;
  balance: number;
  currency: string;
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
 * Build a daily balance series for each banking account by forward-filling
 * the last known balance from statements.
 */
async function getBankingBalanceSeries(
  from: string,
  to: string,
  displayCurrency: Currency
): Promise<{
  series: Map<string, Map<string, number>>;
  accountNames: Map<string, string>;
}> {
  const allAccounts = await db.select().from(accounts);
  const bankingAccounts = allAccounts.filter((a) => a.type !== "investment");

  const series = new Map<string, Map<string, number>>();
  const accountNames = new Map<string, string>();

  for (const account of bankingAccounts) {
    accountNames.set(account.id, account.name);

    const txns = await db
      .select({
        date: bankingTransactions.date,
        balance: bankingTransactions.balance,
        currency: bankingTransactions.currency,
      })
      .from(bankingTransactions)
      .where(eq(bankingTransactions.accountId, account.id))
      .orderBy(asc(bankingTransactions.date));

    if (txns.length === 0) continue;

    // Build date→balance map from transactions that have balance
    const dateBalanceMap = new Map<string, { balance: number; currency: string }>();
    for (const tx of txns) {
      if (tx.balance !== null) {
        const d = tx.date.toISOString().split("T")[0];
        dateBalanceMap.set(d, {
          balance: parseFloat(tx.balance),
          currency: tx.currency,
        });
      }
    }

    // Forward-fill through the date range
    const dates = getAllDatesBetween(from, to);
    const accountSeries = new Map<string, number>();
    let lastBalance = 0;
    let lastCurrency = account.baseCurrency;

    // Get FX rates for this account's currencies
    const currencies = new Set([...dateBalanceMap.values()].map((v) => v.currency));
    const fxMaps = new Map<string, Map<string, number>>();

    for (const cur of currencies) {
      if (cur !== displayCurrency) {
        const fxMap = await getFxRateRange(from, to, cur as Currency, displayCurrency);
        fxMaps.set(cur, fxMap);
      }
    }

    for (const date of dates) {
      const entry = dateBalanceMap.get(date);
      if (entry) {
        lastBalance = entry.balance;
        lastCurrency = entry.currency;
      }

      const fx = lastCurrency === displayCurrency
        ? 1
        : (fxMaps.get(lastCurrency)?.get(date) ?? 1);

      accountSeries.set(date, Math.round(lastBalance * fx * 100) / 100);
    }

    series.set(account.id, accountSeries);
  }

  return { series, accountNames };
}

export async function getNetWorthTimeSeries(opts: {
  from?: string;
  to?: string;
  currency: Currency;
}): Promise<NetWorthPoint[]> {
  const to = opts.to || new Date().toISOString().split("T")[0];

  // Get investment portfolio time series
  const investmentSeries = await getPortfolioTimeSeries({
    currency: opts.currency,
    from: opts.from,
    to,
  });

  // Determine start date
  const from = opts.from ||
    (investmentSeries.length > 0 ? investmentSeries[0].date : "2022-01-01");

  // Get banking balance series
  const { series: bankingSeries, accountNames } = await getBankingBalanceSeries(
    from, to, opts.currency
  );

  const dates = getAllDatesBetween(from, to);
  const investmentByDate = new Map(
    investmentSeries.map((p) => [p.date, p.portfolioValue])
  );

  const points: NetWorthPoint[] = [];

  for (const date of dates) {
    const acctValues: Record<string, number> = {};
    let total = 0;

    // Investment value
    const investmentValue = investmentByDate.get(date) ?? 0;
    if (investmentValue > 0) {
      acctValues["Investments"] = investmentValue;
      total += investmentValue;
    }

    // Banking account values
    for (const [accountId, dailyBalances] of bankingSeries) {
      const balance = dailyBalances.get(date) ?? 0;
      const name = accountNames.get(accountId) || accountId;
      acctValues[name] = balance;
      total += balance;
    }

    points.push({
      date,
      total: Math.round(total * 100) / 100,
      accounts: acctValues,
    });
  }

  return points;
}

export async function getCurrentBalances(
  currency: Currency
): Promise<AccountBalance[]> {
  const allAccounts = await db.select().from(accounts);
  const bankingAccounts = allAccounts.filter((a) => a.type !== "investment");

  const balances: AccountBalance[] = [];

  for (const account of bankingAccounts) {
    // Get the most recent transaction with a balance
    const latest = await db
      .select({
        balance: bankingTransactions.balance,
        currency: bankingTransactions.currency,
      })
      .from(bankingTransactions)
      .where(eq(bankingTransactions.accountId, account.id))
      .orderBy(asc(bankingTransactions.date))
      .limit(1);

    // Use the last transaction for this account
    const allTxns = await db
      .select({
        balance: bankingTransactions.balance,
        currency: bankingTransactions.currency,
        date: bankingTransactions.date,
      })
      .from(bankingTransactions)
      .where(eq(bankingTransactions.accountId, account.id))
      .orderBy(asc(bankingTransactions.date));

    let lastBalance = 0;
    let lastCurrency = account.baseCurrency;
    for (const tx of allTxns) {
      if (tx.balance !== null) {
        lastBalance = parseFloat(tx.balance);
        lastCurrency = tx.currency;
      }
    }

    balances.push({
      accountId: account.id,
      accountName: account.name,
      accountType: account.type,
      balance: lastBalance,
      currency: lastCurrency,
    });
  }

  return balances;
}
