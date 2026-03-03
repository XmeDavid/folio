"use client";

import { useEffect, useState, useCallback } from "react";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { LoadingSpinner } from "@/components/ui/loading";
import { formatMoney, pnlColor } from "@/lib/utils";
import { SpendingChart } from "@/components/charts/spending-chart";
import { MonthlyTrendChart } from "@/components/charts/monthly-trend-chart";
import { TrendingDown, TrendingUp, Wallet, Calendar } from "lucide-react";

interface SpendingData {
  totalSpending: number;
  totalIncome: number;
  net: number;
  byCategory: { category: string; total: number; count: number }[];
  byMonth: { month: string; spending: number; income: number; net: number }[];
  topMerchants: { merchant: string; total: number; count: number }[];
}

type Period = "1M" | "3M" | "6M" | "1Y" | "ALL";

function getPeriodDates(period: Period): { from?: string; to?: string } {
  if (period === "ALL") return {};
  const now = new Date();
  const to = now.toISOString().split("T")[0];
  const from = new Date(now);
  switch (period) {
    case "1M":
      from.setMonth(from.getMonth() - 1);
      break;
    case "3M":
      from.setMonth(from.getMonth() - 3);
      break;
    case "6M":
      from.setMonth(from.getMonth() - 6);
      break;
    case "1Y":
      from.setFullYear(from.getFullYear() - 1);
      break;
  }
  return { from: from.toISOString().split("T")[0], to };
}

export default function SpendingPage() {
  const [data, setData] = useState<SpendingData | null>(null);
  const [loading, setLoading] = useState(true);
  const [period, setPeriod] = useState<Period>("3M");
  const currency = "CHF";

  const fetchData = useCallback(async () => {
    setLoading(true);
    const { from, to } = getPeriodDates(period);
    const params = new URLSearchParams();
    if (from) params.set("from", from);
    if (to) params.set("to", to);

    const res = await fetch(`/api/banking/spending?${params}`);
    const json = await res.json();
    setData(json);
    setLoading(false);
  }, [period]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  if (loading) return <LoadingSpinner />;
  if (!data) return null;

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div>
          <h2 className="text-2xl font-semibold tracking-tight">Spending</h2>
          <p className="text-sm text-text-tertiary mt-0.5">
            Spending analysis across banking accounts
          </p>
        </div>

        <div className="flex items-center bg-bg-tertiary rounded-lg p-0.5 border border-border-subtle">
          {(["1M", "3M", "6M", "1Y", "ALL"] as Period[]).map((p) => (
            <button
              key={p}
              onClick={() => setPeriod(p)}
              className={`px-3 py-1.5 text-xs font-mono font-medium rounded-md transition-all ${
                period === p
                  ? "bg-accent text-bg-primary shadow-sm"
                  : "text-text-tertiary hover:text-text-secondary"
              }`}
            >
              {p}
            </button>
          ))}
        </div>
      </div>

      {/* Summary cards */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <Card>
          <CardContent className="pt-4">
            <div className="flex items-center gap-2 mb-1">
              <TrendingDown size={14} className="text-red" />
              <span className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider">
                Total Spending
              </span>
            </div>
            <p className="text-2xl font-semibold font-mono text-red">
              {formatMoney(Math.abs(data.totalSpending), currency)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-4">
            <div className="flex items-center gap-2 mb-1">
              <TrendingUp size={14} className="text-green" />
              <span className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider">
                Total Income
              </span>
            </div>
            <p className="text-2xl font-semibold font-mono text-green">
              {formatMoney(data.totalIncome, currency)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-4">
            <div className="flex items-center gap-2 mb-1">
              <Wallet size={14} className="text-accent" />
              <span className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider">
                Net
              </span>
            </div>
            <p className={`text-2xl font-semibold font-mono ${pnlColor(data.net)}`}>
              {formatMoney(data.net, currency)}
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Monthly trend */}
      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <Calendar size={16} className="text-accent" />
            <CardTitle>Monthly Income vs Spending</CardTitle>
          </div>
        </CardHeader>
        <CardContent>
          <MonthlyTrendChart data={data.byMonth} currency={currency} />
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Category breakdown */}
        <Card>
          <CardHeader>
            <CardTitle>Spending by Category</CardTitle>
          </CardHeader>
          <CardContent>
            <SpendingChart data={data.byCategory} currency={currency} />
          </CardContent>
        </Card>

        {/* Top merchants */}
        <Card>
          <CardHeader>
            <CardTitle>Top Merchants</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-2">
              {data.topMerchants.slice(0, 15).map((m) => (
                <div
                  key={m.merchant}
                  className="flex items-center justify-between py-1.5 border-b border-border-subtle last:border-0"
                >
                  <div>
                    <span className="text-sm text-text-primary">{m.merchant}</span>
                    <span className="text-[10px] font-mono text-text-tertiary ml-2">
                      {m.count}x
                    </span>
                  </div>
                  <span className="font-mono text-sm text-red">
                    {formatMoney(Math.abs(m.total), currency)}
                  </span>
                </div>
              ))}
              {data.topMerchants.length === 0 && (
                <p className="text-sm text-text-tertiary font-mono">No merchant data</p>
              )}
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
