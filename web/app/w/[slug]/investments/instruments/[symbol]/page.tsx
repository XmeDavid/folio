"use client";

import * as React from "react";
import { use } from "react";
import Link from "next/link";
import type { Route } from "next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  Trash2,
} from "lucide-react";
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
import { ErrorBanner, LoadingText } from "@/components/app/empty";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  deleteDividend,
  deleteTrade,
  fetchInstrumentDetail,
  type DividendEvent,
  type InstrumentDetail,
  type Trade,
} from "@/lib/api/investments";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";

export default function InstrumentDetailPage({
  params,
}: {
  params: Promise<{ slug: string; symbol: string }>;
}) {
  const { slug, symbol } = use(params);
  const decodedSymbol = decodeURIComponent(symbol);
  const workspace = useCurrentWorkspace(slug);
  const workspaceId = workspace?.id ?? null;
  const queryClient = useQueryClient();

  const detailQuery = useQuery({
    queryKey: ["investments", "instrument", workspaceId, decodedSymbol],
    queryFn: () => fetchInstrumentDetail(workspaceId!, decodedSymbol),
    enabled: !!workspaceId,
  });

  const deleteTradeMutation = useMutation({
    mutationFn: (tradeId: string) => deleteTrade(workspaceId!, tradeId),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ["investments"] }),
  });

  const deleteDividendMutation = useMutation({
    mutationFn: (dividendId: string) =>
      deleteDividend(workspaceId!, dividendId),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ["investments"] }),
  });

  if (!workspace) return null;

  const detail = detailQuery.data;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <Link
          href={`/w/${slug}/investments/positions` as Route}
          className="inline-flex w-fit items-center gap-1 text-[12px] text-fg-muted hover:text-fg hover:underline"
        >
          <ArrowLeft className="h-3 w-3" /> Positions
        </Link>
        <PageHeader
          eyebrow="Investments"
          title={detail ? detail.instrument.symbol : decodedSymbol}
          description={detail?.instrument.name}
        />
      </div>

      {detailQuery.isLoading ? (
        <LoadingText>Loading instrument…</LoadingText>
      ) : detailQuery.isError ? (
        <ErrorBanner
          title="Couldn't load instrument"
          description={(detailQuery.error as Error).message}
        />
      ) : !detail ? null : (
        <>
          <InstrumentSummary detail={detail} />
          <HoldingsOverTime detail={detail} />
          <div className="grid gap-4 lg:grid-cols-2">
            <TradesCard
              trades={detail.trades}
              onDelete={(id) =>
                window.confirm("Delete this trade? Position will be replayed.") &&
                deleteTradeMutation.mutate(id)
              }
            />
            <DividendsCard
              dividends={detail.dividends}
              onDelete={(id) =>
                window.confirm("Delete this dividend?") &&
                deleteDividendMutation.mutate(id)
              }
            />
          </div>
        </>
      )}
    </div>
  );
}

function InstrumentSummary({ detail }: { detail: InstrumentDetail }) {
  const inst = detail.instrument;
  const totalQty = detail.positions.reduce(
    (s, p) => s + Number(p.quantity || 0),
    0
  );
  const totalCost = detail.positions.reduce(
    (s, p) => s + Number(p.costBasisTotal || 0),
    0
  );
  const realised = detail.positions.reduce(
    (s, p) => s + Number(p.realisedPnL || 0),
    0
  );
  const dividends = detail.positions.reduce(
    (s, p) => s + Number(p.dividendsReceived || 0),
    0
  );
  return (
    <Card>
      <CardContent className="grid gap-3 p-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat
          label="Asset class"
          value={inst.assetClass.replace("_", " ")}
          mono={false}
          bold
        />
        <Stat
          label="Currency"
          value={inst.currency}
          sub={inst.exchange ?? undefined}
        />
        <Stat
          label="Last quote"
          value={
            detail.lastQuote
              ? formatAmount(detail.lastQuote.price, detail.lastQuote.currency)
              : "—"
          }
          sub={
            detail.lastQuote
              ? `${detail.lastQuote.source} · ${formatDate(detail.lastQuote.asOf)}${detail.lastQuote.stale ? " · stale" : ""}`
              : undefined
          }
        />
        <Stat
          label="Open quantity"
          value={totalQty.toString()}
          sub={`Cost ${formatAmount(totalCost.toString(), inst.currency)}`}
        />
        <Stat
          label="Realised P/L"
          value={formatAmount(realised.toString(), inst.currency)}
          accent={realised < 0 ? "neg" : realised > 0 ? "pos" : "neutral"}
        />
        <Stat
          label="Dividends received"
          value={formatAmount(dividends.toString(), inst.currency)}
          accent="pos"
        />
      </CardContent>
    </Card>
  );
}

function Stat({
  label,
  value,
  sub,
  accent,
  bold,
  mono = true,
}: {
  label: string;
  value: string;
  sub?: string;
  accent?: "pos" | "neg" | "neutral";
  bold?: boolean;
  mono?: boolean;
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <div className="text-[11px] font-medium tracking-wide text-fg-faint uppercase">
        {label}
      </div>
      <div
        className={
          (mono ? "tabular-nums " : "") +
          (bold ? "text-[15px] font-medium capitalize " : "text-[15px] ") +
          (accent === "pos"
            ? "text-emerald-500"
            : accent === "neg"
              ? "text-rose-500"
              : "text-fg")
        }
      >
        {value}
      </div>
      {sub ? <div className="text-[11px] text-fg-muted">{sub}</div> : null}
    </div>
  );
}

function HoldingsOverTime({ detail }: { detail: InstrumentDetail }) {
  // Build series: for every trade boundary we already have qty; for price-only
  // points we use price * qty when both are available.
  const points = detail.history
    .map((p) => ({
      date: new Date(p.date).getTime(),
      qty: Number(p.quantity || 0),
      value: p.value ? Number(p.value) : null,
      price: p.price ? Number(p.price) : null,
    }))
    .filter((p) => Number.isFinite(p.date))
    .sort((a, b) => a.date - b.date);
  if (points.length < 2) {
    return null;
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>Holding over time</CardTitle>
      </CardHeader>
      <CardContent className="h-[260px] p-2">
        <ResponsiveContainer width="100%" height="100%">
          <RechartsLineChart data={points}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" />
            <XAxis
              dataKey="date"
              type="number"
              domain={["dataMin", "dataMax"]}
              tickFormatter={(v) => new Date(v).toLocaleDateString()}
              fontSize={11}
              stroke="var(--color-fg-muted)"
            />
            <YAxis fontSize={11} stroke="var(--color-fg-muted)" />
            <Tooltip
              labelFormatter={(v) => new Date(Number(v)).toLocaleDateString()}
              contentStyle={{
                fontSize: 12,
                background: "var(--color-surface)",
                border: "1px solid var(--color-border)",
                borderRadius: 8,
              }}
            />
            <Line
              type="monotone"
              dataKey="value"
              stroke="var(--color-accent)"
              strokeWidth={2}
              dot={false}
              connectNulls
              name="Market value"
            />
            <Line
              type="stepAfter"
              dataKey="qty"
              stroke="var(--color-fg-muted)"
              strokeWidth={1.5}
              dot={false}
              name="Quantity"
            />
          </RechartsLineChart>
        </ResponsiveContainer>
      </CardContent>
    </Card>
  );
}

function TradesCard({
  trades,
  onDelete,
}: {
  trades: Trade[];
  onDelete: (tradeId: string) => void;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Trade history</CardTitle>
      </CardHeader>
      <CardContent className="overflow-x-auto p-0">
        {trades.length === 0 ? (
          <p className="px-4 py-6 text-[13px] text-fg-muted">
            No trades recorded.
          </p>
        ) : (
          <table className="w-full text-[13px]">
            <thead className="text-[11px] text-fg-muted uppercase tracking-wide">
              <tr className="border-b border-border">
                <th className="px-4 py-2 text-left font-medium">Date</th>
                <th className="px-2 py-2 text-left font-medium">Side</th>
                <th className="px-2 py-2 text-right font-medium">Qty</th>
                <th className="px-2 py-2 text-right font-medium">Price</th>
                <th className="px-2 py-2 text-right font-medium">Fee</th>
                <th className="px-2 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {trades.map((t) => (
                <tr
                  key={t.id}
                  className="border-b border-border last:border-b-0"
                >
                  <td className="px-4 py-2">{formatDate(t.tradeDate)}</td>
                  <td className="px-2 py-2">
                    <Badge
                      variant={t.side === "buy" ? "accent" : "neutral"}
                      className="capitalize"
                    >
                      {t.side}
                    </Badge>
                  </td>
                  <td className="px-2 py-2 text-right tabular-nums">
                    {t.quantity}
                  </td>
                  <td className="px-2 py-2 text-right tabular-nums">
                    {formatAmount(t.price, t.currency)}
                  </td>
                  <td className="px-2 py-2 text-right tabular-nums text-fg-muted">
                    {formatAmount(t.feeAmount, t.feeCurrency)}
                  </td>
                  <td className="px-2 py-2 text-right">
                    <button
                      onClick={() => onDelete(t.id)}
                      className="text-fg-muted hover:text-rose-500"
                      aria-label="Delete trade"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </CardContent>
    </Card>
  );
}

function DividendsCard({
  dividends,
  onDelete,
}: {
  dividends: DividendEvent[];
  onDelete: (dividendId: string) => void;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Dividends</CardTitle>
      </CardHeader>
      <CardContent className="overflow-x-auto p-0">
        {dividends.length === 0 ? (
          <p className="px-4 py-6 text-[13px] text-fg-muted">
            No dividend events recorded.
          </p>
        ) : (
          <table className="w-full text-[13px]">
            <thead className="text-[11px] text-fg-muted uppercase tracking-wide">
              <tr className="border-b border-border">
                <th className="px-4 py-2 text-left font-medium">Pay date</th>
                <th className="px-2 py-2 text-right font-medium">Per unit</th>
                <th className="px-2 py-2 text-right font-medium">Total</th>
                <th className="px-2 py-2 text-right font-medium">Withheld</th>
                <th className="px-2 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {dividends.map((d) => (
                <tr
                  key={d.id}
                  className="border-b border-border last:border-b-0"
                >
                  <td className="px-4 py-2">{formatDate(d.payDate)}</td>
                  <td className="px-2 py-2 text-right tabular-nums">
                    {formatAmount(d.amountPerUnit, d.currency)}
                  </td>
                  <td className="px-2 py-2 text-right tabular-nums text-emerald-500">
                    {formatAmount(d.totalAmount, d.currency)}
                  </td>
                  <td className="px-2 py-2 text-right tabular-nums text-fg-muted">
                    {formatAmount(d.taxWithheld, d.currency)}
                  </td>
                  <td className="px-2 py-2 text-right">
                    <button
                      onClick={() => onDelete(d.id)}
                      className="text-fg-muted hover:text-rose-500"
                      aria-label="Delete dividend"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </CardContent>
    </Card>
  );
}
