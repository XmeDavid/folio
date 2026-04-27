"use client";

import * as React from "react";
import { use } from "react";
import Link from "next/link";
import type { Route } from "next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LineChart, RefreshCcw, ArrowUpRight } from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  fetchDashboard,
  refreshInvestments,
  type Holding,
} from "@/lib/api/investments";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { formatAmount } from "@/lib/format";

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

  const dashboardQuery = useQuery({
    queryKey: ["investments", "dashboard", workspaceId, reportCcy],
    queryFn: () =>
      fetchDashboard(workspaceId!, { currency: reportCcy }),
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
              {refreshMutation.isPending ? "Refreshing…" : "Refresh"}
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
        <LoadingText>Loading dashboard…</LoadingText>
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
              description={data.warnings.join(" · ")}
            />
          ) : null}

          <SummaryGrid summary={data} reportCcy={reportCcy} />

          <div className="grid gap-4 lg:grid-cols-3">
            <AllocationCard
              title="By currency"
              slices={data.allocationByCurrency}
              reportCcy={reportCcy}
            />
            <AllocationCard
              title="By asset class"
              slices={data.allocationByAssetClass}
              reportCcy={reportCcy}
            />
            <MoversCard movers={data.topMovers} />
          </div>

          <Card>
            <CardHeader>
              <CardTitle>Holdings</CardTitle>
            </CardHeader>
            <CardContent className="overflow-x-auto p-0">
              <HoldingsTable holdings={data.holdings} slug={slug} />
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}

function SummaryGrid({
  summary,
  reportCcy,
}: {
  summary: ReturnType<typeof Object.assign> & {
    totalMarketValue: string;
    totalCostBasis: string;
    totalUnrealisedPnL: string;
    totalUnrealisedPnLPct: string;
    totalRealisedPnL: string;
    totalDividends: string;
    totalFees: string;
    totalReturn: string;
    totalReturnPct: string;
    openPositionsCount: number;
    staleQuotes: number;
    missingQuotes: number;
  };
  reportCcy: string;
}) {
  const stat = (
    label: string,
    value: string,
    sub?: string,
    accent?: "pos" | "neg" | "neutral"
  ) => (
    <div className="flex flex-col gap-1 rounded-[12px] border border-border bg-surface px-4 py-3">
      <div className="text-[11px] font-medium tracking-wide text-fg-faint uppercase">
        {label}
      </div>
      <div
        className={
          "text-[20px] font-medium tabular-nums " +
          (accent === "pos"
            ? "text-emerald-500"
            : accent === "neg"
              ? "text-rose-500"
              : "text-fg")
        }
      >
        {value}
      </div>
      {sub ? <div className="text-[12px] text-fg-muted">{sub}</div> : null}
    </div>
  );
  const sign = (n: string): "pos" | "neg" | "neutral" => {
    const t = n.trim();
    if (t.startsWith("-")) return "neg";
    const num = Number(t);
    if (!Number.isFinite(num) || num === 0) return "neutral";
    return "pos";
  };
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {stat(
        "Market value",
        formatAmount(summary.totalMarketValue, reportCcy),
        `${summary.openPositionsCount} open · ${summary.staleQuotes} stale`
      )}
      {stat(
        "Unrealised P/L",
        formatAmount(summary.totalUnrealisedPnL, reportCcy),
        `${summary.totalUnrealisedPnLPct}% on cost`,
        sign(summary.totalUnrealisedPnL)
      )}
      {stat(
        "Realised P/L",
        formatAmount(summary.totalRealisedPnL, reportCcy),
        "lifetime",
        sign(summary.totalRealisedPnL)
      )}
      {stat(
        "Total return",
        formatAmount(summary.totalReturn, reportCcy),
        `${summary.totalReturnPct}% incl. dividends · fees ${formatAmount(summary.totalFees, reportCcy)}`,
        sign(summary.totalReturn)
      )}
    </div>
  );
}

function AllocationCard({
  title,
  slices,
  reportCcy,
}: {
  title: string;
  slices: { key: string; label: string; value: string; pct: string }[];
  reportCcy: string;
}) {
  if (slices.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{title}</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-[13px] text-fg-muted">No exposure to show yet.</p>
        </CardContent>
      </Card>
    );
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {slices.slice(0, 6).map((s) => (
          <div key={s.key} className="flex flex-col gap-1">
            <div className="flex items-center justify-between text-[13px]">
              <span className="font-medium text-fg">{s.label}</span>
              <span className="tabular-nums text-fg-muted">
                {formatAmount(s.value, reportCcy)} · {s.pct}%
              </span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-border">
              <div
                className="h-full rounded-full bg-accent"
                style={{ width: `${Math.min(100, Number(s.pct))}%` }}
              />
            </div>
          </div>
        ))}
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
          <p className="text-[13px] text-fg-muted">
            No live quotes yet. Refresh once positions exist.
          </p>
        ) : (
          movers.map((m) => {
            const neg = m.unrealisedPnL.trim().startsWith("-");
            return (
              <div
                key={m.symbol}
                className="flex items-center justify-between text-[13px]"
              >
                <div className="flex flex-col gap-0.5">
                  <span className="font-medium text-fg">{m.symbol}</span>
                  <span className="text-[11px] text-fg-muted">{m.name}</span>
                </div>
                <div
                  className={
                    "tabular-nums text-right " +
                    (neg ? "text-rose-500" : "text-emerald-500")
                  }
                >
                  <div>
                    {formatAmount(m.unrealisedPnL, m.reportCurrency)}
                  </div>
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

function HoldingsTable({
  holdings,
  slug,
}: {
  holdings: Holding[];
  slug: string;
}) {
  if (holdings.length === 0) {
    return (
      <p className="px-4 py-6 text-[13px] text-fg-muted">
        No holdings yet.
      </p>
    );
  }
  return (
    <table className="w-full text-[13px]">
      <thead className="text-[11px] text-fg-muted uppercase tracking-wide">
        <tr className="border-b border-border">
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
        {holdings.map((h) => {
          const unr = h.unrealisedPnLReport ?? "0";
          const tr = h.totalReturnReport ?? "0";
          return (
            <tr
              key={`${h.accountId}:${h.instrumentId}`}
              className="border-b border-border last:border-b-0"
            >
              <td className="px-4 py-2">
                <div className="flex flex-col">
                  <span className="font-medium text-fg">{h.symbol}</span>
                  <span className="text-[11px] text-fg-muted">{h.name}</span>
                </div>
              </td>
              <td className="px-2 py-2 text-right tabular-nums">
                {h.quantity}
              </td>
              <td className="px-2 py-2 text-right tabular-nums text-fg-muted">
                {h.lastPrice
                  ? formatAmount(h.lastPrice, h.instrumentCurrency)
                  : "—"}
              </td>
              <td className="px-2 py-2 text-right tabular-nums">
                {h.marketValueReport
                  ? formatAmount(h.marketValueReport, h.reportCurrency)
                  : "—"}
              </td>
              <td className="px-2 py-2 text-right tabular-nums text-fg-muted">
                {formatAmount(h.costBasisReport, h.reportCurrency)}
              </td>
              <td
                className={
                  "px-2 py-2 text-right tabular-nums " +
                  (unr.trim().startsWith("-")
                    ? "text-rose-500"
                    : "text-emerald-500")
                }
              >
                {h.unrealisedPnLReport
                  ? formatAmount(h.unrealisedPnLReport, h.reportCurrency)
                  : "—"}
              </td>
              <td
                className={
                  "px-2 py-2 text-right tabular-nums " +
                  (tr.trim().startsWith("-")
                    ? "text-rose-500"
                    : "text-emerald-500")
                }
              >
                {h.totalReturnReport
                  ? formatAmount(h.totalReturnReport, h.reportCurrency)
                  : "—"}
                {h.totalReturnPercentReport ? (
                  <span className="ml-1 text-[11px] text-fg-muted">
                    ({h.totalReturnPercentReport}%)
                  </span>
                ) : null}
              </td>
              <td className="px-4 py-2 text-right">
                <Link
                  href={`/w/${slug}/investments/instruments/${encodeURIComponent(h.symbol)}` as Route}
                  className="inline-flex items-center gap-1 text-[12px] text-fg hover:underline"
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
