"use client";

import * as React from "react";
import { use } from "react";
import Link from "next/link";
import type { Route } from "next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpRight, LineChart, RefreshCcw } from "lucide-react";
import {
  CartesianGrid,
  Line,
  LineChart as RechartsLineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  fetchDashboard,
  fetchDashboardHistory,
  fetchDividends,
  fetchTrades,
  refreshInvestments,
  type DashboardSummary,
  type DividendEvent,
  type Holding,
  type PortfolioHistoryPoint,
  type Trade,
} from "@/lib/api/investments";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";

const HISTORY_RANGES = ["1W", "1M", "3M", "6M", "YTD", "1Y", "ALL"] as const;
type HistoryRange = (typeof HISTORY_RANGES)[number];
const HISTORY_STALE_MS = 5 * 60 * 1000;
const HISTORY_GC_MS = 15 * 60 * 1000;

export default function InvestmentsDashboardPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  const workspaceId = workspace?.id ?? null;
  const reportCcy = workspace?.baseCurrency ?? "CHF";
  const queryClient = useQueryClient();
  const [historyRange, setHistoryRange] = React.useState<HistoryRange>("1M");
  const [holdingFilter, setHoldingFilter] = React.useState("all");

  const dashboardQuery = useQuery({
    queryKey: ["investments", "dashboard", workspaceId, reportCcy],
    queryFn: () => fetchDashboard(workspaceId!, { currency: reportCcy }),
    enabled: !!workspaceId,
  });

  const historyQuery = useQuery({
    queryKey: [
      "investments",
      "dashboard-history",
      workspaceId,
      reportCcy,
      historyRange,
    ],
    queryFn: () =>
      fetchDashboardHistory(workspaceId!, {
        currency: reportCcy,
        range: historyRange,
      }),
    enabled: !!workspaceId,
    staleTime: HISTORY_STALE_MS,
    gcTime: HISTORY_GC_MS,
  });

  React.useEffect(() => {
    if (!workspaceId || !historyQuery.isSuccess) return;
    let cancelled = false;
    const ranges = HISTORY_RANGES.filter((range) => range !== historyRange);
    const preload = () => {
      if (cancelled) return;
      ranges.forEach((range, index) => {
        window.setTimeout(() => {
          if (cancelled) return;
          void queryClient.prefetchQuery({
            queryKey: [
              "investments",
              "dashboard-history",
              workspaceId,
              reportCcy,
              range,
            ],
            queryFn: () =>
              fetchDashboardHistory(workspaceId, {
                currency: reportCcy,
                range,
              }),
            staleTime: HISTORY_STALE_MS,
            gcTime: HISTORY_GC_MS,
          });
        }, index * 250);
      });
    };

    const idleWindow = window as Window & {
      requestIdleCallback?: (
        cb: IdleRequestCallback,
        opts?: IdleRequestOptions
      ) => number;
      cancelIdleCallback?: (handle: number) => void;
    };
    const scheduledWithIdle =
      typeof idleWindow.requestIdleCallback === "function";
    const idleHandle = scheduledWithIdle
      ? idleWindow.requestIdleCallback!(preload, { timeout: 2500 })
      : window.setTimeout(preload, 1500);

    return () => {
      cancelled = true;
      if (
        scheduledWithIdle &&
        typeof idleWindow.cancelIdleCallback === "function"
      ) {
        idleWindow.cancelIdleCallback(idleHandle);
      } else {
        window.clearTimeout(idleHandle);
      }
    };
  }, [
    historyQuery.dataUpdatedAt,
    historyQuery.isSuccess,
    historyRange,
    queryClient,
    reportCcy,
    workspaceId,
  ]);

  const tradesQuery = useQuery({
    queryKey: ["investments", "trades", workspaceId],
    queryFn: () => fetchTrades(workspaceId!),
    enabled: !!workspaceId,
  });

  const dividendsQuery = useQuery({
    queryKey: ["investments", "dividends", workspaceId],
    queryFn: () => fetchDividends(workspaceId!),
    enabled: !!workspaceId,
  });

  const refreshMutation = useMutation({
    mutationFn: () => refreshInvestments(workspaceId!),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ["investments"] }),
  });

  if (!workspace) return null;

  const data = dashboardQuery.data;

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Portfolio"
        title="Investments"
        description="Total value, returns, and exposure across every brokerage account in this workspace."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="secondary"
              onClick={() => refreshMutation.mutate()}
              disabled={refreshMutation.isPending}
            >
              <RefreshCcw className="h-4 w-4" />
              {refreshMutation.isPending ? "Refreshing..." : "Refresh"}
            </Button>
            <Button asChild>
              <Link href={`/w/${slug}/investments/positions` as Route}>
                <LineChart className="h-4 w-4" />
                Positions
              </Link>
            </Button>
          </div>
        }
      />

      {dashboardQuery.isLoading ? (
        <LoadingText>Loading dashboard...</LoadingText>
      ) : dashboardQuery.isError ? (
        <ErrorBanner
          title="Couldn't load investment dashboard"
          description={(dashboardQuery.error as Error).message}
        />
      ) : !data ? null : data.holdings.length === 0 ? (
        <EmptyState
          title="No investment activity yet"
          description="Add a brokerage account, then record trades or import a Revolut Trading / IBKR Flex statement to see your portfolio here."
          action={
            <Button asChild>
              <Link href={`/w/${slug}/accounts` as Route}>Open an account</Link>
            </Button>
          }
        />
      ) : (
        <>
          {data.warnings && data.warnings.length > 0 ? (
            <ErrorBanner
              title="Pricing or FX gaps"
              description={data.warnings.join(" / ")}
            />
          ) : null}

          <SummaryGrid summary={data} reportCcy={reportCcy} />

          <div className="grid gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(320px,1fr)]">
            <PerformanceCard
              data={historyQuery.data ?? []}
              isLoading={historyQuery.isLoading}
              range={historyRange}
              onRangeChange={setHistoryRange}
              reportCcy={reportCcy}
              summary={data}
            />
            <MoversCard movers={data.topMovers} />
          </div>

          <HoldingsCard
            holdings={data.holdings}
            slug={slug}
            filter={holdingFilter}
            onFilterChange={setHoldingFilter}
          />

          <RecentActivityCard
            trades={tradesQuery.data ?? []}
            dividends={dividendsQuery.data ?? []}
          />
        </>
      )}
    </div>
  );
}

function SummaryGrid({
  summary,
  reportCcy,
}: {
  summary: DashboardSummary;
  reportCcy: string;
}) {
  const stats = [
    {
      label: "Cost basis",
      value: formatAmount(summary.totalCostBasis, reportCcy),
      sub: `${summary.openPositionsCount} open positions`,
      tone: "neutral" as const,
    },
    {
      label: "Unrealised P/L",
      value: formatAmount(summary.totalUnrealisedPnL, reportCcy),
      sub: `${summary.totalUnrealisedPnLPct}% on cost`,
      tone: sign(summary.totalUnrealisedPnL),
    },
    {
      label: "Realised P/L",
      value: formatAmount(summary.totalRealisedPnL, reportCcy),
      sub: "lifetime",
      tone: sign(summary.totalRealisedPnL),
    },
    {
      label: "Dividends",
      value: formatAmount(summary.totalDividends, reportCcy),
      sub: `fees ${formatAmount(summary.totalFees, reportCcy)}`,
      tone: sign(summary.totalDividends),
    },
    {
      label: "Total return",
      value: formatAmount(summary.totalReturn, reportCcy),
      sub: `${summary.totalReturnPct}% incl. dividends and fees`,
      tone: sign(summary.totalReturn),
    },
  ];

  return (
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
      {stats.map((stat) => (
        <div
          key={stat.label}
          className="border-border bg-surface flex min-h-[108px] flex-col justify-between rounded-[8px] border px-4 py-3"
        >
          <div className="text-fg-faint text-[11px] font-medium tracking-wide uppercase">
            {stat.label}
          </div>
          <div
            className={
              "text-[20px] font-medium tabular-nums " + toneClass(stat.tone)
            }
          >
            {stat.value}
          </div>
          <div className="text-fg-muted text-[12px]">{stat.sub}</div>
        </div>
      ))}
    </div>
  );
}

function PerformanceCard({
  data,
  isLoading,
  range,
  onRangeChange,
  reportCcy,
  summary,
}: {
  data: PortfolioHistoryPoint[];
  isLoading: boolean;
  range: HistoryRange;
  onRangeChange: (range: HistoryRange) => void;
  reportCcy: string;
  summary: DashboardSummary;
}) {
  const chartData = data
    .map((point) => ({
      date: point.date,
      value: Number(point.value),
    }))
    .filter((point) => Number.isFinite(point.value));

  return (
    <Card>
      <CardHeader className="gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex flex-col gap-1">
          <CardTitle>Portfolio performance</CardTitle>
          <div className="text-fg text-[24px] font-medium tabular-nums">
            {formatAmount(summary.totalMarketValue, reportCcy)}
          </div>
          <div
            className={
              "text-[13px] tabular-nums " + toneClass(sign(summary.totalReturn))
            }
          >
            {formatAmount(summary.totalReturn, reportCcy)} /{" "}
            {summary.totalReturnPct}%
          </div>
        </div>
        <div className="border-border bg-surface-subtle flex flex-wrap gap-1 rounded-[8px] border p-1">
          {HISTORY_RANGES.map((option) => (
            <button
              key={option}
              type="button"
              onClick={() => onRangeChange(option)}
              className={
                "h-7 rounded-[6px] px-2 text-[12px] font-medium tabular-nums transition-colors " +
                (range === option
                  ? "bg-surface text-fg shadow-sm"
                  : "text-fg-muted hover:text-fg")
              }
            >
              {option}
            </button>
          ))}
        </div>
      </CardHeader>
      <CardContent>
        <div className="h-[280px] w-full">
          {isLoading ? (
            <LoadingText>Loading performance...</LoadingText>
          ) : chartData.length === 0 ? (
            <p className="text-fg-muted py-8 text-[13px]">
              No priced history for this range.
            </p>
          ) : (
            <ResponsiveContainer width="100%" height="100%">
              <RechartsLineChart
                data={chartData}
                margin={{ top: 12, right: 12, bottom: 0, left: 0 }}
              >
                <CartesianGrid stroke="var(--color-border)" vertical={false} />
                <XAxis
                  dataKey="date"
                  tick={{ fill: "var(--color-fg-muted)", fontSize: 12 }}
                  tickLine={false}
                  axisLine={false}
                  tickFormatter={(value) =>
                    new Intl.DateTimeFormat(undefined, {
                      month: "short",
                      day: "numeric",
                    }).format(new Date(value))
                  }
                />
                <YAxis
                  width={72}
                  tick={{ fill: "var(--color-fg-muted)", fontSize: 12 }}
                  tickLine={false}
                  axisLine={false}
                  tickFormatter={(value) => compactCurrency(value, reportCcy)}
                />
                <Tooltip
                  cursor={{ stroke: "var(--color-border-strong)" }}
                  contentStyle={{
                    border: "1px solid var(--color-border)",
                    borderRadius: 8,
                    background: "var(--color-surface)",
                    color: "var(--color-fg)",
                  }}
                  formatter={(value) => [
                    formatAmount(String(value), reportCcy),
                    "Value",
                  ]}
                  labelFormatter={(value) => formatDate(String(value))}
                />
                <Line
                  type="monotone"
                  dataKey="value"
                  stroke="var(--color-accent)"
                  strokeWidth={2}
                  dot={false}
                  activeDot={{ r: 4 }}
                />
              </RechartsLineChart>
            </ResponsiveContainer>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

function MoversCard({
  movers,
}: {
  movers: {
    symbol: string;
    name: string;
    unrealisedPnL: string;
    unrealisedPct: string;
    reportCurrency: string;
  }[];
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Top movers</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {movers.length === 0 ? (
          <p className="text-fg-muted text-[13px]">
            No live quotes yet. Refresh once positions exist.
          </p>
        ) : (
          movers.map((m) => {
            const tone = sign(m.unrealisedPnL);
            return (
              <div
                key={m.symbol}
                className="flex items-center justify-between gap-3 text-[13px]"
              >
                <div className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-fg font-medium">{m.symbol}</span>
                  <span className="text-fg-muted truncate text-[11px]">
                    {m.name}
                  </span>
                </div>
                <div
                  className={
                    "shrink-0 text-right tabular-nums " + toneClass(tone)
                  }
                >
                  <div>{formatAmount(m.unrealisedPnL, m.reportCurrency)}</div>
                  <div className="text-[11px] opacity-80">
                    {m.unrealisedPct}%
                  </div>
                </div>
              </div>
            );
          })
        )}
      </CardContent>
    </Card>
  );
}

function RecentActivityCard({
  trades,
  dividends,
}: {
  trades: Trade[];
  dividends: DividendEvent[];
}) {
  const items = React.useMemo(() => {
    const tradeItems = trades.map((trade) => {
      const quantity = Number(trade.quantity);
      const price = Number(trade.price);
      const fee = Number(trade.feeAmount || 0);
      const amount = Number.isFinite(quantity * price + fee)
        ? quantity * price + fee
        : 0;
      return {
        key: trade.id,
        date: trade.tradeDate,
        title: `${trade.side === "buy" ? "Buy" : "Sell"} ${trade.symbol}`,
        detail: `${trade.quantity} @ ${formatAmount(trade.price, trade.currency)}`,
        amount: `${trade.side === "buy" ? "-" : "+"}${amount.toFixed(2)}`,
        currency: trade.currency,
        tone: trade.side === "buy" ? "neg" : "pos",
      };
    });
    const dividendItems = dividends.map((dividend) => ({
      key: dividend.id,
      date: dividend.payDate,
      title: `Dividend ${dividend.symbol}`,
      detail: formatDate(dividend.payDate),
      amount: dividend.totalAmount,
      currency: dividend.currency,
      tone: "pos",
    }));
    return [...tradeItems, ...dividendItems]
      .sort((a, b) => new Date(b.date).getTime() - new Date(a.date).getTime())
      .slice(0, 6);
  }, [trades, dividends]);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Recent activity</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {items.length === 0 ? (
          <p className="text-fg-muted text-[13px]">No activity yet.</p>
        ) : (
          items.map((item) => (
            <div
              key={item.key}
              className="flex items-center justify-between gap-3 text-[13px]"
            >
              <div className="flex min-w-0 flex-col gap-0.5">
                <span className="text-fg truncate font-medium">
                  {item.title}
                </span>
                <span className="text-fg-muted truncate text-[11px]">
                  {item.detail}
                </span>
              </div>
              <div className={"shrink-0 tabular-nums " + toneClass(item.tone)}>
                {formatAmount(item.amount, item.currency)}
              </div>
            </div>
          ))
        )}
      </CardContent>
    </Card>
  );
}

function HoldingsCard({
  holdings,
  slug,
  filter,
  onFilterChange,
}: {
  holdings: Holding[];
  slug: string;
  filter: string;
  onFilterChange: (filter: string) => void;
}) {
  const openHoldings = holdings.filter((h) => Number(h.quantity || 0) > 0);
  const classes = Array.from(
    new Set(openHoldings.map((h) => h.assetClass).filter(Boolean))
  ).sort();
  const filters = ["all", ...classes];

  return (
    <Card>
      <CardHeader className="gap-3 sm:flex-row sm:items-center sm:justify-between">
        <CardTitle>Holdings</CardTitle>
        <div className="border-border bg-surface-subtle flex flex-wrap gap-1 rounded-[8px] border p-1">
          {filters.map((option) => (
            <button
              key={option}
              type="button"
              onClick={() => onFilterChange(option)}
              className={
                "h-7 rounded-[6px] px-2 text-[12px] font-medium transition-colors " +
                (filter === option
                  ? "bg-surface text-fg shadow-sm"
                  : "text-fg-muted hover:text-fg")
              }
            >
              {option === "all" ? "All" : formatAssetClass(option)}
            </button>
          ))}
        </div>
      </CardHeader>
      <CardContent className="overflow-x-auto p-0">
        <HoldingsTable holdings={openHoldings} slug={slug} filter={filter} />
      </CardContent>
    </Card>
  );
}

function HoldingsTable({
  holdings,
  slug,
  filter,
}: {
  holdings: Holding[];
  slug: string;
  filter: string;
}) {
  const visibleHoldings =
    filter === "all"
      ? holdings
      : holdings.filter((h) => h.assetClass === filter);

  if (visibleHoldings.length === 0) {
    return (
      <p className="text-fg-muted px-4 py-6 text-[13px]">No open holdings.</p>
    );
  }
  return (
    <table className="w-full text-[13px]">
      <thead className="text-fg-muted text-[11px] tracking-wide uppercase">
        <tr className="border-border border-b">
          <th className="px-4 py-2 text-left font-medium">Symbol</th>
          <th className="px-2 py-2 text-right font-medium">Qty</th>
          <th className="px-2 py-2 text-right font-medium">Price</th>
          <th className="px-2 py-2 text-right font-medium">Market value</th>
          <th className="px-2 py-2 text-right font-medium">Cost basis</th>
          <th className="px-2 py-2 text-right font-medium">Unrealised</th>
          <th className="px-2 py-2 text-right font-medium">Total return</th>
          <th className="px-4 py-2"></th>
        </tr>
      </thead>
      <tbody>
        {visibleHoldings.map((h) => {
          const unr = h.unrealisedPnLReport ?? "0";
          const tr = h.totalReturnReport ?? "0";
          return (
            <tr
              key={`${h.accountId}:${h.instrumentId}`}
              className="border-border border-b last:border-b-0"
            >
              <td className="px-4 py-2">
                <div className="flex min-w-[180px] flex-col">
                  <span className="text-fg font-medium">{h.symbol}</span>
                  <span className="text-fg-muted truncate text-[11px]">
                    {h.name}
                  </span>
                </div>
              </td>
              <td className="px-2 py-2 text-right tabular-nums">
                {h.quantity}
              </td>
              <td className="text-fg-muted px-2 py-2 text-right tabular-nums">
                {h.lastPrice
                  ? formatAmount(h.lastPrice, h.instrumentCurrency)
                  : "-"}
              </td>
              <td className="px-2 py-2 text-right tabular-nums">
                {h.marketValueReport
                  ? formatAmount(h.marketValueReport, h.reportCurrency)
                  : "-"}
              </td>
              <td className="text-fg-muted px-2 py-2 text-right tabular-nums">
                {formatAmount(h.costBasisReport, h.reportCurrency)}
              </td>
              <td
                className={
                  "px-2 py-2 text-right tabular-nums " + toneClass(sign(unr))
                }
              >
                {h.unrealisedPnLReport
                  ? formatAmount(h.unrealisedPnLReport, h.reportCurrency)
                  : "-"}
              </td>
              <td
                className={
                  "px-2 py-2 text-right tabular-nums " + toneClass(sign(tr))
                }
              >
                {h.totalReturnReport
                  ? formatAmount(h.totalReturnReport, h.reportCurrency)
                  : "-"}
                {h.totalReturnPercentReport ? (
                  <span className="text-fg-muted ml-1 text-[11px]">
                    ({h.totalReturnPercentReport}%)
                  </span>
                ) : null}
              </td>
              <td className="px-4 py-2 text-right">
                <Link
                  href={
                    `/w/${slug}/investments/instruments/${encodeURIComponent(h.symbol)}` as Route
                  }
                  className="text-fg inline-flex items-center gap-1 text-[12px] hover:underline"
                >
                  Drill <ArrowUpRight className="h-3 w-3" />
                </Link>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function sign(n: string): "pos" | "neg" | "neutral" {
  const trimmed = n.trim();
  if (trimmed.startsWith("-")) return "neg";
  const value = Number(trimmed);
  if (!Number.isFinite(value) || value === 0) return "neutral";
  return "pos";
}

function toneClass(tone: "pos" | "neg" | "neutral" | string) {
  if (tone === "pos") return "text-emerald-500";
  if (tone === "neg") return "text-rose-500";
  return "text-fg";
}

function formatAssetClass(value: string) {
  return value
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1).toLowerCase())
    .join(" ");
}

function compactCurrency(value: number, currency: string) {
  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency,
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(value);
}
