"use client";

import { useEffect, useState, useCallback } from "react";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { CurrencyToggle, type DisplayCurrency } from "@/components/ui/currency-toggle";
import { LoadingSpinner } from "@/components/ui/loading";
import { TimeSeriesChart } from "@/components/charts/time-series-chart";
import { formatMoney, pnlColor } from "@/lib/utils";
import { Landmark, TrendingUp, Wallet, CreditCard, ArrowUpDown } from "lucide-react";

interface NetWorthPoint {
  date: string;
  total: number;
  accounts: Record<string, number>;
}

interface AccountBalance {
  accountId: string;
  accountName: string;
  accountType: string;
  balance: number;
  currency: string;
}

interface SpendingData {
  totalSpending: number;
  totalIncome: number;
  net: number;
  byMonth: { month: string; spending: number; income: number; net: number }[];
}

type Timeframe = "3M" | "1Y" | "3Y" | "ALL";

function getFromDate(tf: Timeframe): string | undefined {
  if (tf === "ALL") return undefined;
  const d = new Date();
  switch (tf) {
    case "3M": d.setMonth(d.getMonth() - 3); break;
    case "1Y": d.setFullYear(d.getFullYear() - 1); break;
    case "3Y": d.setFullYear(d.getFullYear() - 3); break;
  }
  return d.toISOString().split("T")[0];
}

// Generate distinct colors for account lines
const ACCOUNT_COLORS = [
  "#6366f1", "#22c55e", "#f59e0b", "#ef4444",
  "#8b5cf6", "#06b6d4", "#ec4899", "#14b8a6",
];

export default function OverviewPage() {
  const [currency, setCurrency] = useState<DisplayCurrency>("CHF");
  const [timeframe, setTimeframe] = useState<Timeframe>("1Y");
  const [netWorthSeries, setNetWorthSeries] = useState<NetWorthPoint[]>([]);
  const [balances, setBalances] = useState<AccountBalance[]>([]);
  const [spending, setSpending] = useState<SpendingData | null>(null);
  const [recentTxns, setRecentTxns] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchData = useCallback(async () => {
    setLoading(true);
    const from = getFromDate(timeframe);
    const params = new URLSearchParams({ currency });
    if (from) params.set("from", from);

    const [nwRes, balRes, spendRes, recentRes] = await Promise.all([
      fetch(`/api/banking/networth?${params}`),
      fetch(`/api/banking/networth?view=balances&currency=${currency}`),
      fetch(`/api/banking/spending?from=${from || ""}&to=`),
      fetch(`/api/banking/transactions?limit=20&offset=0&sortBy=date&sortDir=desc`),
    ]);

    const [nwJson, balJson, spendJson, recentJson] = await Promise.all([
      nwRes.json(),
      balRes.json(),
      spendRes.json(),
      recentRes.json(),
    ]);

    setNetWorthSeries(nwJson.series || []);
    setBalances(balJson.balances || []);
    setSpending(spendJson);
    setRecentTxns(recentJson.data || []);
    setLoading(false);
  }, [currency, timeframe]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  if (loading) return <LoadingSpinner />;

  // Current net worth = last point in series
  const currentNetWorth = netWorthSeries.length > 0
    ? netWorthSeries[netWorthSeries.length - 1].total
    : 0;

  // Account names from the series for the chart
  const accountNames = netWorthSeries.length > 0
    ? Object.keys(netWorthSeries[netWorthSeries.length - 1].accounts)
    : [];

  // Prepare time series chart data
  const chartData = netWorthSeries.map((p) => ({
    date: p.date,
    Total: p.total,
    ...p.accounts,
  }));

  const chartSeries = [
    { key: "Total", color: "#6366f1", name: "Net Worth" },
  ];

  // Current month spending
  const now = new Date();
  const currentMonth = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`;
  const thisMonthSpending = spending?.byMonth.find((m) => m.month === currentMonth);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-semibold tracking-tight">Overview</h2>
          <p className="text-sm text-text-tertiary mt-0.5">
            Net worth and financial overview
          </p>
        </div>
        <div className="flex items-center gap-3">
          <div className="flex items-center bg-bg-tertiary rounded-lg p-0.5 border border-border-subtle">
            {(["3M", "1Y", "3Y", "ALL"] as Timeframe[]).map((tf) => (
              <button
                key={tf}
                onClick={() => setTimeframe(tf)}
                className={`px-3 py-1.5 text-xs font-mono font-medium rounded-md transition-all ${
                  timeframe === tf
                    ? "bg-accent text-bg-primary shadow-sm"
                    : "text-text-tertiary hover:text-text-secondary"
                }`}
              >
                {tf}
              </button>
            ))}
          </div>
          <CurrencyToggle value={currency} onChange={setCurrency} />
        </div>
      </div>

      {/* Summary cards */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <Card>
          <CardContent className="pt-4">
            <div className="flex items-center gap-2 mb-1">
              <Landmark size={14} className="text-accent" />
              <span className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider">
                Net Worth
              </span>
            </div>
            <p className="text-2xl font-semibold font-mono text-text-primary">
              {formatMoney(currentNetWorth, currency)}
            </p>
          </CardContent>
        </Card>

        {balances.slice(0, 3).map((b) => (
          <Card key={b.accountId}>
            <CardContent className="pt-4">
              <div className="flex items-center gap-2 mb-1">
                <Wallet size={14} className="text-text-tertiary" />
                <span className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider truncate">
                  {b.accountName}
                </span>
              </div>
              <p className="text-lg font-semibold font-mono text-text-primary">
                {formatMoney(b.balance, b.currency)}
              </p>
              <span className="text-[10px] font-mono text-text-tertiary uppercase">
                {b.accountType}
              </span>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Net worth chart */}
      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <TrendingUp size={16} className="text-accent" />
            <CardTitle>Net Worth</CardTitle>
          </div>
        </CardHeader>
        <CardContent>
          <TimeSeriesChart
            data={chartData}
            series={chartSeries}
            currency={currency}
            height={360}
          />
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Monthly summary */}
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <CreditCard size={16} className="text-accent" />
              <CardTitle>This Month</CardTitle>
            </div>
          </CardHeader>
          <CardContent>
            {thisMonthSpending ? (
              <div className="space-y-3">
                <div className="flex justify-between items-center">
                  <span className="text-sm text-text-secondary">Income</span>
                  <span className="font-mono text-sm text-green">
                    {formatMoney(thisMonthSpending.income, currency)}
                  </span>
                </div>
                <div className="flex justify-between items-center">
                  <span className="text-sm text-text-secondary">Spending</span>
                  <span className="font-mono text-sm text-red">
                    {formatMoney(Math.abs(thisMonthSpending.spending), currency)}
                  </span>
                </div>
                <div className="border-t border-border-subtle pt-2 flex justify-between items-center">
                  <span className="text-sm font-medium text-text-primary">Net</span>
                  <span className={`font-mono text-sm font-medium ${pnlColor(thisMonthSpending.net)}`}>
                    {formatMoney(thisMonthSpending.net, currency)}
                  </span>
                </div>
                {thisMonthSpending.income > 0 && (
                  <div className="flex justify-between items-center">
                    <span className="text-sm text-text-tertiary">Savings Rate</span>
                    <span className="font-mono text-sm text-text-secondary">
                      {((thisMonthSpending.net / thisMonthSpending.income) * 100).toFixed(1)}%
                    </span>
                  </div>
                )}
              </div>
            ) : (
              <p className="text-sm text-text-tertiary font-mono">No data for current month</p>
            )}
          </CardContent>
        </Card>

        {/* Recent activity */}
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <ArrowUpDown size={16} className="text-accent" />
              <CardTitle>Recent Activity</CardTitle>
            </div>
          </CardHeader>
          <CardContent>
            <div className="space-y-1">
              {recentTxns.slice(0, 10).map((r: any) => {
                const tx = r.transaction;
                const amount = parseFloat(tx.amount);
                const d = new Date(tx.date);
                return (
                  <div
                    key={tx.id}
                    className="flex items-center justify-between py-1.5 border-b border-border-subtle last:border-0"
                  >
                    <div className="flex-1 min-w-0">
                      <p className="text-sm text-text-primary truncate">
                        {tx.merchant || tx.description}
                      </p>
                      <p className="text-[10px] font-mono text-text-tertiary">
                        {d.toLocaleDateString("en-GB", {
                          day: "2-digit",
                          month: "short",
                        })}{" "}
                        · {r.accountName}
                      </p>
                    </div>
                    <span className={`font-mono text-sm ${pnlColor(amount)}`}>
                      {formatMoney(amount, tx.currency)}
                    </span>
                  </div>
                );
              })}
              {recentTxns.length === 0 && (
                <p className="text-sm text-text-tertiary font-mono">No recent activity</p>
              )}
            </div>
          </CardContent>
        </Card>
      </div>

      {/* All account balances */}
      {balances.length > 3 && (
        <Card>
          <CardHeader>
            <CardTitle>All Account Balances</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-2">
              {balances.map((b) => (
                <div
                  key={b.accountId}
                  className="flex items-center justify-between py-1.5 border-b border-border-subtle last:border-0"
                >
                  <div>
                    <span className="text-sm text-text-primary">{b.accountName}</span>
                    <span className="text-[10px] font-mono text-text-tertiary ml-2 uppercase">
                      {b.accountType}
                    </span>
                  </div>
                  <span className="font-mono text-sm text-text-primary">
                    {formatMoney(b.balance, b.currency)}
                  </span>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
