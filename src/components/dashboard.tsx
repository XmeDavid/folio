"use client";

import { useEffect, useState, useCallback, useMemo } from "react";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import {
  CurrencyToggle,
  type DisplayCurrency,
} from "@/components/ui/currency-toggle";
import { AccountFilter } from "@/components/ui/account-filter";
import { LoadingCard, LoadingSpinner } from "@/components/ui/loading";
import { AllocationChart } from "@/components/charts/allocation-chart";
import { HoldingsTable } from "@/components/charts/holdings-table";
import { TimeSeriesChart } from "@/components/charts/time-series-chart";
import { formatMoney, formatPercent, pnlColor, cn } from "@/lib/utils";
import { usePortfolioCache } from "@/lib/cache-context";
import {
  TrendingUp,
  TrendingDown,
  Wallet,
  BarChart3,
  Coins,
  Receipt,
  RefreshCw,
} from "lucide-react";
import { Tip } from "@/components/ui/tip";

interface PortfolioSummary {
  currency: string;
  fxRate: number;
  totalCurrentValue: number;
  totalCostBasis: number;
  totalUnrealizedPnL: number;
  totalUnrealizedPnLPercent: number;
  totalRealizedPnL: number;
  totalDividends: number;
  totalCommissions: number;
  totalReturn: number;
  totalReturnPercent: number;
  holdings: Array<{
    ticker: string;
    quantity: number;
    avgCostBasis: number;
    currentPrice: number | null;
    currentValue: number;
    unrealizedPnL: number;
    unrealizedPnLPercent: number;
    totalReturn: number;
    totalReturnPercent: number;
    broker: string;
    currency: string;
    dividendsReceived: number;
    totalInvested: number;
    realizedPnL: number;
    commissionsTotal: number;
  }>;
}

interface TimeSeriesPoint {
  date: string;
  portfolioValue: number;
  costBasis: number;
  totalReturn: number;
  priceOnlyReturn: number;
  unrealizedPnL: number;
  realizedPnL: number;
  dividends: number;
  fees: number;
}

type Metric = "total" | "priceOnly";
type Range = "3M" | "1Y" | "3Y" | "ALL";

function StatCard({
  label,
  value,
  subValue,
  icon: Icon,
  colorClass,
  delay,
  tooltip,
}: {
  label: string;
  value: string;
  subValue?: string;
  icon: React.ElementType;
  colorClass?: string;
  delay?: string;
  tooltip?: string;
}) {
  return (
    <Card className={`animate-fade-in opacity-0 ${delay || ""}`}>
      <CardContent className="flex items-start justify-between">
        <div>
          <p className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1.5">
            {tooltip ? <Tip text={tooltip}>{label}</Tip> : label}
          </p>
          <p
            className={cn(
              "text-2xl font-semibold tracking-tight font-mono",
              colorClass || "text-text-primary"
            )}
          >
            {value}
          </p>
          {subValue && (
            <p className={cn("text-sm font-mono mt-0.5", colorClass || "text-text-secondary")}>
              {subValue}
            </p>
          )}
        </div>
        <div className="p-2 rounded-lg bg-bg-tertiary">
          <Icon size={18} className="text-text-tertiary" />
        </div>
      </CardContent>
    </Card>
  );
}

function MiniStat({
  label,
  value,
  colorClass,
  icon: Icon,
  tooltip,
}: {
  label: string;
  value: string;
  colorClass?: string;
  icon: React.ElementType;
  tooltip?: string;
}) {
  return (
    <div className="flex items-center gap-3 px-4 py-3">
      <Icon size={14} className="text-text-tertiary shrink-0" />
      <div className="min-w-0">
        <p className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider">
          {tooltip ? <Tip text={tooltip}>{label}</Tip> : label}
        </p>
        <p className={cn("text-sm font-mono font-semibold", colorClass || "text-text-primary")}>
          {value}
        </p>
      </div>
    </div>
  );
}

function rangeCutoffDate(range: Range): string | undefined {
  if (range === "ALL") return undefined;
  const d = new Date();
  if (range === "3M") d.setMonth(d.getMonth() - 3);
  else if (range === "1Y") d.setFullYear(d.getFullYear() - 1);
  else if (range === "3Y") d.setFullYear(d.getFullYear() - 3);
  return d.toISOString().split("T")[0];
}

const RANGES: Range[] = ["3M", "1Y", "3Y", "ALL"];

export default function Dashboard() {
  const cache = usePortfolioCache();
  const [currency, setCurrency] = useState<DisplayCurrency>("CHF");
  const [accountId, setAccountId] = useState<string | undefined>(undefined);
  const [data, setData] = useState<PortfolioSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [fullTimeSeries, setFullTimeSeries] = useState<TimeSeriesPoint[]>([]);
  const [tsLoading, setTsLoading] = useState(false);
  const [metric, setMetric] = useState<Metric>("total");
  const [range, setRange] = useState<Range>("ALL");

  const summaryKey = `summary|${currency}|${accountId ?? "ALL"}`;
  const tsKey = `timeseries|${currency}|${accountId ?? "ALL"}`;

  const fetchData = useCallback(
    async (cur: DisplayCurrency, accId?: string, force?: boolean) => {
      const key = `summary|${cur}|${accId ?? "ALL"}`;
      if (!force) {
        const cached = cache.get<PortfolioSummary>(key);
        if (cached) {
          setData(cached);
          setLoading(false);
          return;
        }
      }
      setLoading(true);
      setError(null);
      try {
        const params = new URLSearchParams({ currency: cur });
        if (accId) params.set("accountId", accId);
        const res = await fetch(`/api/portfolio?${params}`);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const json: PortfolioSummary = await res.json();
        cache.set(key, json);
        setData(json);
      } catch (err) {
        setError(String(err));
      } finally {
        setLoading(false);
      }
    },
    [cache]
  );

  const fetchTimeSeries = useCallback(
    async (cur: DisplayCurrency, accId?: string, force?: boolean) => {
      const key = `timeseries|${cur}|${accId ?? "ALL"}`;
      if (!force) {
        const cached = cache.get<TimeSeriesPoint[]>(key);
        if (cached) {
          setFullTimeSeries(cached);
          setTsLoading(false);
          return;
        }
      }
      setTsLoading(true);
      try {
        const params = new URLSearchParams({ view: "timeseries", currency: cur });
        if (accId) params.set("accountId", accId);
        const res = await fetch(`/api/portfolio?${params}`);
        if (res.ok) {
          const json: TimeSeriesPoint[] = await res.json();
          cache.set(key, json);
          setFullTimeSeries(json);
        }
      } catch {
        /* non-critical */
      } finally {
        setTsLoading(false);
      }
    },
    [cache]
  );

  useEffect(() => {
    fetchData(currency, accountId);
    fetchTimeSeries(currency, accountId);
  }, [currency, accountId, fetchData, fetchTimeSeries]);

  const visibleTimeSeries = useMemo(() => {
    const cutoff = rangeCutoffDate(range);
    if (!cutoff) return fullTimeSeries;
    return fullTimeSeries.filter((p) => p.date >= cutoff);
  }, [fullTimeSeries, range]);

  const handleRefresh = useCallback(() => {
    cache.invalidateAll();
    fetchData(currency, accountId, true);
    fetchTimeSeries(currency, accountId, true);
  }, [currency, accountId, fetchData, fetchTimeSeries, cache]);

  if (error) {
    return (
      <div className="flex items-center justify-center h-[60vh]">
        <Card className="max-w-md">
          <CardContent>
            <p className="text-red font-mono text-sm">{error}</p>
            <button
              onClick={handleRefresh}
              className="mt-4 px-4 py-2 bg-accent text-bg-primary rounded-lg text-sm font-medium"
            >
              Retry
            </button>
          </CardContent>
        </Card>
      </div>
    );
  }

  const chartSeries =
    metric === "total"
      ? [
          { key: "portfolioValue", color: "#6c9cff", name: "Value" },
          { key: "costBasis", color: "#5c6278", name: "Cost Basis" },
          { key: "totalReturn", color: "#3dd68c", name: "Total Return" },
        ]
      : [
          { key: "portfolioValue", color: "#6c9cff", name: "Value" },
          { key: "costBasis", color: "#5c6278", name: "Cost Basis" },
          { key: "priceOnlyReturn", color: "#ffc145", name: "Price Return" },
        ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h2 className="text-2xl font-semibold tracking-tight">Dashboard</h2>
          <p className="text-sm text-text-tertiary mt-0.5">
            Portfolio overview and performance
          </p>
        </div>
        <div className="flex items-center gap-3 flex-wrap">
          <AccountFilter value={accountId} onChange={setAccountId} />
          <button
            onClick={handleRefresh}
            className="p-2 rounded-lg bg-bg-tertiary border border-border-subtle text-text-tertiary hover:text-text-primary hover:bg-bg-hover transition-colors"
            title="Refresh (bypass cache)"
          >
            <RefreshCw size={16} className={loading || tsLoading ? "animate-spin" : ""} />
          </button>
          <CurrencyToggle value={currency} onChange={setCurrency} />
        </div>
      </div>

      {loading || !data ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <LoadingCard key={i} />
          ))}
        </div>
      ) : (
        <>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
            <StatCard
              label="Portfolio Value"
              tooltip="Current market value of all open positions, converted to display currency"
              value={formatMoney(data.totalCurrentValue, currency)}
              icon={Wallet}
              delay="stagger-1"
            />
            <StatCard
              label="Unrealized P&L"
              tooltip="Profit/loss on positions you still hold. Current value minus cost basis"
              value={formatMoney(data.totalUnrealizedPnL, currency)}
              subValue={formatPercent(data.totalUnrealizedPnLPercent)}
              icon={data.totalUnrealizedPnL >= 0 ? TrendingUp : TrendingDown}
              colorClass={pnlColor(data.totalUnrealizedPnL)}
              delay="stagger-2"
            />
            <StatCard
              label="Total Return"
              tooltip="Unrealized + realized + dividends - fees. Your complete all-time performance"
              value={formatMoney(data.totalReturn, currency)}
              subValue={formatPercent(data.totalReturnPercent)}
              icon={BarChart3}
              colorClass={pnlColor(data.totalReturn)}
              delay="stagger-3"
            />
            <StatCard
              label="Dividends"
              tooltip="Total dividends received across all positions"
              value={formatMoney(data.totalDividends, currency)}
              icon={Coins}
              colorClass="text-yellow"
              delay="stagger-4"
            />
          </div>

          <Card className="animate-fade-in opacity-0 stagger-2">
            <div className="grid grid-cols-1 sm:grid-cols-3 divide-y sm:divide-y-0 sm:divide-x divide-border-subtle">
              <MiniStat
                label="Cost Basis"
                tooltip="Total amount invested in current open positions at purchase price"
                value={formatMoney(data.totalCostBasis, currency)}
                icon={Receipt}
              />
              <MiniStat
                label="Realized P&L"
                tooltip="Profit/loss from positions you have already sold or closed"
                value={formatMoney(data.totalRealizedPnL, currency)}
                colorClass={pnlColor(data.totalRealizedPnL)}
                icon={TrendingUp}
              />
              <MiniStat
                label="Total Commissions"
                tooltip="Sum of all broker commissions and fees paid"
                value={formatMoney(data.totalCommissions, currency)}
                colorClass="text-red"
                icon={Receipt}
              />
            </div>
          </Card>

          <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
            <Card className="lg:col-span-2 animate-fade-in opacity-0 stagger-3">
              <CardHeader>
                <div className="flex items-center justify-between flex-wrap gap-3">
                  <CardTitle>Performance Over Time</CardTitle>
                  <div className="flex items-center gap-2">
                    <div className="flex items-center bg-bg-tertiary rounded-lg p-0.5 border border-border-subtle">
                      {(["total", "priceOnly"] as Metric[]).map((m) => (
                        <button
                          key={m}
                          onClick={() => setMetric(m)}
                          title={
                            m === "total"
                              ? "Unrealized + realized + dividends - fees"
                              : "Unrealized + realized only (no dividends/fees)"
                          }
                          className={cn(
                            "px-2.5 py-1 text-[10px] font-mono font-medium rounded-md transition-all",
                            metric === m
                              ? "bg-accent text-bg-primary"
                              : "text-text-tertiary hover:text-text-secondary"
                          )}
                        >
                          {m === "total" ? "Total" : "Price"}
                        </button>
                      ))}
                    </div>
                    <div className="flex items-center bg-bg-tertiary rounded-lg p-0.5 border border-border-subtle">
                      {RANGES.map((r) => (
                        <button
                          key={r}
                          onClick={() => setRange(r)}
                          className={cn(
                            "px-2.5 py-1 text-[10px] font-mono font-medium rounded-md transition-all",
                            range === r
                              ? "bg-accent text-bg-primary"
                              : "text-text-tertiary hover:text-text-secondary"
                          )}
                        >
                          {r}
                        </button>
                      ))}
                    </div>
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                {tsLoading ? (
                  <div className="h-[320px] flex items-center justify-center">
                    <LoadingSpinner />
                  </div>
                ) : (
                  <TimeSeriesChart
                    data={visibleTimeSeries}
                    series={chartSeries}
                    currency={currency}
                  />
                )}
              </CardContent>
            </Card>

            <Card className="animate-fade-in opacity-0 stagger-4">
              <CardHeader>
                <CardTitle>Allocation</CardTitle>
              </CardHeader>
              <CardContent>
                <AllocationChart
                  holdings={data.holdings}
                  currency={currency}
                />
              </CardContent>
            </Card>
          </div>

          <Card className="animate-fade-in opacity-0 stagger-5">
            <CardHeader>
              <div className="flex items-center justify-between">
                <CardTitle>Holdings</CardTitle>
                <span className="text-xs font-mono text-text-tertiary">
                  {data.holdings.length} positions
                </span>
              </div>
            </CardHeader>
            <div className="overflow-hidden">
              <HoldingsTable
                holdings={data.holdings}
                displayCurrency={currency}
              />
            </div>
          </Card>
        </>
      )}
    </div>
  );
}
