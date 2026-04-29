"use client";

import * as React from "react";
import { use } from "react";
import Link from "next/link";
import type { Route } from "next";
import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, Search } from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { QuantityText } from "@/components/investments/quantity-text";
import { fetchPositions, type Position } from "@/lib/api/investments";
import { fetchAccounts, type Account } from "@/lib/api/client";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";

type Filter = "all" | "open" | "closed";

export default function PositionsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  const workspaceId = workspace?.id ?? null;
  const [filter, setFilter] = React.useState<Filter>("open");
  const [accountId, setAccountId] = React.useState<string>("");
  const [search, setSearch] = React.useState("");

  const positionsQuery = useQuery({
    queryKey: [
      "investments",
      "positions",
      workspaceId,
      filter,
      accountId,
      search,
    ],
    queryFn: () =>
      fetchPositions(workspaceId!, {
        status: filter === "all" ? undefined : filter,
        accountId: accountId || undefined,
        search: search || undefined,
      }),
    enabled: !!workspaceId,
  });

  const accountsQuery = useQuery({
    queryKey: ["accounts", workspaceId],
    queryFn: () => fetchAccounts(workspaceId!),
    enabled: !!workspaceId,
  });

  if (!workspace) return null;
  const brokerageAccounts: Account[] =
    accountsQuery.data?.filter((a) => a.kind === "brokerage") ?? [];

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow="Investments"
        title="Positions"
        description="Open and closed positions across all brokerage accounts. Drilldown to see trades, dividends, and history."
      />

      <div className="flex flex-wrap items-center gap-2">
        <FilterToggle
          options={[
            { id: "open", label: "Open" },
            { id: "closed", label: "Closed" },
            { id: "all", label: "All" },
          ]}
          value={filter}
          onChange={(v) => setFilter(v as Filter)}
        />
        <select
          className="border-border bg-surface rounded-[8px] border px-3 py-1.5 text-[13px]"
          value={accountId}
          onChange={(e) => setAccountId(e.target.value)}
        >
          <option value="">All accounts</option>
          {brokerageAccounts.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name} ({a.currency})
            </option>
          ))}
        </select>
        <div className="relative">
          <Search className="text-fg-muted pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search symbol or name"
            className="w-[220px] pl-8"
          />
        </div>
      </div>

      {positionsQuery.isLoading ? (
        <LoadingText />
      ) : positionsQuery.isError ? (
        <ErrorBanner
          title="Couldn't load positions"
          description={(positionsQuery.error as Error).message}
        />
      ) : (positionsQuery.data ?? []).length === 0 ? (
        <EmptyState
          title="No positions"
          description={
            filter === "open"
              ? "No open holdings match your filters."
              : "No matching positions yet."
          }
        />
      ) : (
        <Card>
          <CardContent className="overflow-x-auto p-0">
            <PositionsTable positions={positionsQuery.data ?? []} slug={slug} />
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function FilterToggle({
  options,
  value,
  onChange,
}: {
  options: { id: string; label: string }[];
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="border-border bg-surface flex rounded-[8px] border p-0.5">
      {options.map((o) => (
        <button
          key={o.id}
          onClick={() => onChange(o.id)}
          className={
            "rounded-[6px] px-3 py-1 text-[13px] font-medium transition " +
            (value === o.id
              ? "bg-page text-fg shadow-sm"
              : "text-fg-muted hover:text-fg")
          }
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

function PositionsTable({
  positions,
  slug,
}: {
  positions: Position[];
  slug: string;
}) {
  return (
    <table className="w-full text-[13px]">
      <thead className="text-fg-muted text-[11px] tracking-wide uppercase">
        <tr className="border-border border-b">
          <th className="px-4 py-2 text-left font-medium">Symbol</th>
          <th className="px-2 py-2 text-left font-medium">Class</th>
          <th className="px-2 py-2 text-right font-medium">Qty</th>
          <th className="px-2 py-2 text-right font-medium">Avg cost</th>
          <th className="px-2 py-2 text-right font-medium">Last price</th>
          <th className="px-2 py-2 text-right font-medium">Market value</th>
          <th className="px-2 py-2 text-right font-medium">Realised</th>
          <th className="px-2 py-2 text-right font-medium">Dividends</th>
          <th className="px-2 py-2 text-right font-medium">Last trade</th>
          <th className="px-4 py-2"></th>
        </tr>
      </thead>
      <tbody>
        {positions.map((p) => (
          <tr
            key={`${p.accountId}:${p.instrumentId}`}
            className="border-border border-b last:border-b-0"
          >
            <td className="px-4 py-2">
              <div className="flex flex-col">
                <span className="text-fg font-medium">{p.symbol}</span>
                <span className="text-fg-muted text-[11px]">{p.name}</span>
              </div>
            </td>
            <td className="px-2 py-2">
              <Badge variant="neutral" className="capitalize">
                {p.assetClass.replace("_", " ")}
              </Badge>
            </td>
            <td className="px-2 py-2 text-right tabular-nums">
              <QuantityText value={p.quantity} />
            </td>
            <td className="text-fg-muted px-2 py-2 text-right tabular-nums">
              {formatAmount(p.averageCost, p.instrumentCurrency)}
            </td>
            <td className="px-2 py-2 text-right tabular-nums">
              {p.lastPrice
                ? formatAmount(p.lastPrice, p.instrumentCurrency)
                : "—"}
            </td>
            <td className="px-2 py-2 text-right tabular-nums">
              {p.marketValue
                ? formatAmount(p.marketValue, p.instrumentCurrency)
                : "—"}
            </td>
            <td
              className={
                "px-2 py-2 text-right tabular-nums " +
                (p.realisedPnL.trim().startsWith("-")
                  ? "text-rose-500"
                  : "text-emerald-500")
              }
            >
              {formatAmount(p.realisedPnL, p.instrumentCurrency)}
            </td>
            <td className="px-2 py-2 text-right text-emerald-500 tabular-nums">
              {formatAmount(p.dividendsReceived, p.instrumentCurrency)}
            </td>
            <td className="text-fg-muted px-2 py-2 text-right text-[12px]">
              {formatDate(p.lastTradeDate)}
            </td>
            <td className="px-4 py-2 text-right">
              <Link
                href={
                  `/w/${slug}/investments/instruments/${encodeURIComponent(p.symbol)}` as Route
                }
                className="text-fg inline-flex items-center gap-1 text-[12px] hover:underline"
              >
                Drill <ArrowUpRight className="h-3 w-3" />
              </Link>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
