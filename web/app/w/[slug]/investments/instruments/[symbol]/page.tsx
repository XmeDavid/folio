"use client";

import * as React from "react";
import { use } from "react";
import Link from "next/link";
import type { Route } from "next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Trash2 } from "lucide-react";
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
import { QuantityText } from "@/components/investments/quantity-text";
import {
  createCorporateAction,
  deleteCorporateAction,
  deleteDividend,
  deleteTrade,
  fetchCorporateActions,
  fetchInstrumentDetail,
  type CorporateAction,
  type CorporateActionKind,
  type DividendEvent,
  type InstrumentDetail,
  type Trade,
} from "@/lib/api/investments";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { DateInput } from "@/components/ui/date-input";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate, formatQuantity } from "@/lib/format";

export default function InstrumentDetailPage({
  params,
}: {
  params: Promise<{ slug: string; symbol: string }>;
}) {
  const { slug, symbol } = use(params);
  const decodedSymbol = decodeURIComponent(symbol);
  const workspace = useCurrentWorkspace(slug);
  const workspaceId = workspace?.id ?? null;
  const [reportCurrencyOverride, setReportCurrencyOverride] =
    React.useState("");
  const reportCurrency =
    reportCurrencyOverride || workspace?.baseCurrency || "CHF";
  const queryClient = useQueryClient();

  const detailQuery = useQuery({
    queryKey: [
      "investments",
      "instrument",
      workspaceId,
      decodedSymbol,
      reportCurrency,
    ],
    queryFn: () =>
      fetchInstrumentDetail(workspaceId!, decodedSymbol, {
        currency: reportCurrency,
      }),
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

  const corporateActionsQuery = useQuery({
    queryKey: [
      "investments",
      "corporate-actions",
      workspaceId,
      detailQuery.data?.instrument.id,
    ],
    queryFn: () =>
      fetchCorporateActions(workspaceId!, detailQuery.data!.instrument.id),
    enabled: !!workspaceId && !!detailQuery.data?.instrument.id,
  });
  const deleteActionMutation = useMutation({
    mutationFn: (actionId: string) =>
      deleteCorporateAction(workspaceId!, actionId),
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
          className="text-fg-muted hover:text-fg inline-flex w-fit items-center gap-1 text-[12px] hover:underline"
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
          <HoldingsOverTime
            detail={detail}
            reportCurrency={reportCurrency}
            onReportCurrencyChange={setReportCurrencyOverride}
          />
          <div className="grid gap-4 lg:grid-cols-2">
            <TradesCard
              trades={detail.trades}
              onDelete={(id) =>
                window.confirm(
                  "Delete this trade? Position will be replayed."
                ) && deleteTradeMutation.mutate(id)
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
          <CorporateActionsCard
            workspaceId={workspaceId!}
            instrumentId={detail.instrument.id}
            currentQuantity={detail.positions.reduce(
              (s, p) => s + Number(p.quantity || 0),
              0
            )}
            actions={corporateActionsQuery.data ?? []}
            onDelete={(id) =>
              window.confirm(
                "Delete this corporate action? Affected positions will be replayed."
              ) && deleteActionMutation.mutate(id)
            }
          />
        </>
      )}
    </div>
  );
}

function CorporateActionsCard({
  workspaceId,
  instrumentId,
  currentQuantity,
  actions,
  onDelete,
}: {
  workspaceId: string;
  instrumentId: string;
  currentQuantity: number;
  actions: CorporateAction[];
  onDelete: (id: string) => void;
}) {
  const queryClient = useQueryClient();
  const [kind, setKind] = React.useState<CorporateActionKind>("reverse_split");
  const [effectiveDate, setEffectiveDate] = React.useState("");
  // Ratio inputs: "ratioNew new shares for every ratioOld old shares".
  // Default for reverse_split is 1-for-50 (a typical reverse-split shape).
  const [ratioNew, setRatioNew] = React.useState("1");
  const [ratioOld, setRatioOld] = React.useState("50");
  const [amount, setAmount] = React.useState("");
  const [newSymbol, setNewSymbol] = React.useState("");

  // When the user toggles between split kinds, pre-fill sensible defaults
  // so the ratio direction matches the kind.
  const switchKind = (k: CorporateActionKind) => {
    setKind(k);
    if (k === "reverse_split") {
      setRatioNew("1");
      setRatioOld("50");
    } else if (k === "split") {
      setRatioNew("4");
      setRatioOld("1");
    }
  };

  const ratioNewN = Number(ratioNew);
  const ratioOldN = Number(ratioOld);
  const factorComputed =
    Number.isFinite(ratioNewN) &&
    Number.isFinite(ratioOldN) &&
    ratioNewN > 0 &&
    ratioOldN > 0
      ? ratioNewN / ratioOldN
      : null;
  const projectedQuantity =
    factorComputed != null && Number.isFinite(currentQuantity)
      ? currentQuantity * factorComputed
      : null;

  const createMutation = useMutation({
    mutationFn: () =>
      createCorporateAction(workspaceId, {
        instrumentId,
        kind,
        effectiveDate,
        // Send the computed factor (new/old) as a high-precision string so
        // backend never has to interpret "ratio" semantics.
        factor:
          factorComputed != null &&
          (kind === "split" || kind === "reverse_split")
            ? factorComputed.toString()
            : undefined,
        amount: amount || undefined,
        newSymbol: newSymbol || undefined,
      }),
    onSuccess: () => {
      setEffectiveDate("");
      setAmount("");
      setNewSymbol("");
      queryClient.invalidateQueries({ queryKey: ["investments"] });
    },
  });

  const needsRatio = kind === "split" || kind === "reverse_split";
  const needsAmount = kind === "cash_distribution" || kind === "delisting";
  const needsSymbol = kind === "symbol_change";

  return (
    <Card>
      <CardHeader>
        <CardTitle>Corporate actions</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="border-border bg-surface rounded-[12px] border p-3 text-[13px]">
          <p className="text-fg font-medium">Add a corporate action</p>
          <p className="text-fg-muted mt-1 text-[12px]">
            Total cost basis is preserved. For a 1-for-50 reverse split, enter{" "}
            <code className="font-mono">1</code> new for every{" "}
            <code className="font-mono">50</code> old. For a 4-for-1 forward
            split, enter <code className="font-mono">4</code> new for every{" "}
            <code className="font-mono">1</code> old.
          </p>
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1">
              <Label htmlFor="ca-kind">Kind</Label>
              <select
                id="ca-kind"
                className="border-border bg-page rounded-[8px] border px-3 py-1.5 text-[13px]"
                value={kind}
                onChange={(e) =>
                  switchKind(e.target.value as CorporateActionKind)
                }
              >
                <option value="reverse_split">Reverse split</option>
                <option value="split">Forward split</option>
                <option value="cash_distribution">Cash distribution</option>
                <option value="delisting">Delisting / closure</option>
                <option value="symbol_change">Symbol change</option>
              </select>
            </div>
            <div className="flex flex-col gap-1">
              <Label htmlFor="ca-date">Effective date</Label>
              <DateInput
                id="ca-date"
                value={effectiveDate}
                onChange={setEffectiveDate}
              />
            </div>
            {needsRatio ? (
              <div className="flex flex-col gap-1 sm:col-span-2">
                <Label>Ratio</Label>
                <div className="flex items-center gap-2 text-[13px]">
                  <Input
                    aria-label="New shares"
                    inputMode="decimal"
                    className="w-24 text-center font-mono tabular-nums"
                    value={ratioNew}
                    onChange={(e) => setRatioNew(e.target.value)}
                  />
                  <span className="text-fg-muted">
                    new share{ratioNewN === 1 ? "" : "s"} for every
                  </span>
                  <Input
                    aria-label="Old shares"
                    inputMode="decimal"
                    className="w-24 text-center font-mono tabular-nums"
                    value={ratioOld}
                    onChange={(e) => setRatioOld(e.target.value)}
                  />
                  <span className="text-fg-muted">old</span>
                </div>
                {factorComputed != null && currentQuantity > 0 ? (
                  <p className="text-fg-muted mt-1 text-[12px]">
                    Your <strong className="text-fg">{currentQuantity}</strong>{" "}
                    shares will become{" "}
                    <strong className="text-fg">
                      {Number.isInteger(projectedQuantity!)
                        ? projectedQuantity
                        : projectedQuantity!.toFixed(8).replace(/\.?0+$/, "")}
                    </strong>
                    .
                  </p>
                ) : null}
              </div>
            ) : null}
            {needsAmount ? (
              <div className="flex flex-col gap-1 sm:col-span-2">
                <Label htmlFor="ca-amount">
                  {kind === "delisting"
                    ? "Cash received (total)"
                    : "Amount per share"}
                </Label>
                <Input
                  id="ca-amount"
                  inputMode="decimal"
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                />
              </div>
            ) : null}
            {needsSymbol ? (
              <div className="flex flex-col gap-1 sm:col-span-2">
                <Label htmlFor="ca-newsym">New symbol</Label>
                <Input
                  id="ca-newsym"
                  value={newSymbol}
                  onChange={(e) => setNewSymbol(e.target.value)}
                />
              </div>
            ) : null}
          </div>
          {createMutation.isError ? (
            <p className="mt-2 text-[12px] text-rose-500">
              {(createMutation.error as Error).message}
            </p>
          ) : null}
          <div className="mt-3 flex justify-end">
            <Button
              onClick={() => createMutation.mutate()}
              disabled={
                createMutation.isPending ||
                !effectiveDate ||
                (needsRatio && factorComputed == null) ||
                (needsAmount && !amount) ||
                (needsSymbol && !newSymbol)
              }
            >
              {createMutation.isPending ? "Saving…" : "Add action"}
            </Button>
          </div>
        </div>

        {actions.length === 0 ? (
          <p className="text-fg-muted text-[13px]">
            No corporate actions on file for this instrument.
          </p>
        ) : (
          <table className="w-full text-[13px]">
            <thead className="text-fg-muted text-[11px] tracking-wide uppercase">
              <tr className="border-border border-b">
                <th className="px-2 py-2 text-left font-medium">Date</th>
                <th className="px-2 py-2 text-left font-medium">Kind</th>
                <th className="px-2 py-2 text-left font-medium">Detail</th>
                <th className="px-2 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {actions.map((a) => (
                <tr
                  key={a.id}
                  className="border-border border-b last:border-b-0"
                >
                  <td className="px-2 py-2">{a.effectiveDate.slice(0, 10)}</td>
                  <td className="px-2 py-2 capitalize">
                    {a.kind.replace("_", " ")}
                  </td>
                  <td className="text-fg-muted px-2 py-2">
                    {(a.kind === "split" || a.kind === "reverse_split") &&
                    a.payload.factor
                      ? `factor ${String(a.payload.factor)}`
                      : a.kind === "cash_distribution" && a.payload.amount
                        ? `amount ${String(a.payload.amount)}`
                        : a.kind === "delisting" && a.payload.cash_total
                          ? `cash ${String(a.payload.cash_total)}`
                          : a.kind === "symbol_change" && a.payload.new_symbol
                            ? `→ ${String(a.payload.new_symbol)}`
                            : ""}
                  </td>
                  <td className="px-2 py-2 text-right">
                    {a.workspaceId ? (
                      <button
                        onClick={() => onDelete(a.id)}
                        className="text-fg-muted hover:text-rose-500"
                        aria-label="Delete corporate action"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    ) : (
                      <span className="text-fg-faint text-[11px]">global</span>
                    )}
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

function InstrumentSummary({ detail }: { detail: InstrumentDetail }) {
  const inst = detail.instrument;
  const openPositions = detail.positions.filter(
    (p) => Number(p.quantity || 0) > 0
  );
  const totalQty = openPositions.reduce(
    (s, p) => s + Number(p.quantity || 0),
    0
  );
  const totalCost = openPositions.reduce(
    (s, p) => s + Number(p.costBasisTotal || 0),
    0
  );
  const totalMarketValue = openPositions.reduce(
    (s, p) => s + (positionMarketValue(p, detail) ?? 0),
    0
  );
  const haveAnyMarketValue = openPositions.some(
    (p) => positionMarketValue(p, detail) !== null
  );
  const realised = detail.positions.reduce(
    (s, p) => s + Number(p.realisedPnL || 0),
    0
  );
  const dividends = detail.positions.reduce(
    (s, p) => s + Number(p.dividendsReceived || 0),
    0
  );
  const unrealised = haveAnyMarketValue ? totalMarketValue - totalCost : null;
  const unrealisedPct =
    unrealised != null && totalCost !== 0
      ? (unrealised / totalCost) * 100
      : null;
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
          value={formatQuantity(totalQty, { maxFractionDigits: 4 })}
          sub={`Cost ${formatAmount(totalCost.toString(), inst.currency)}`}
        />
        <Stat
          label="Market value"
          value={
            haveAnyMarketValue
              ? formatAmount(totalMarketValue.toString(), inst.currency)
              : "—"
          }
          sub={
            haveAnyMarketValue
              ? `${formatQuantity(totalQty, { maxFractionDigits: 4 })} x ${
                  detail.lastQuote
                    ? formatAmount(
                        detail.lastQuote.price,
                        detail.lastQuote.currency
                      )
                    : "—"
                }`
              : "no live quote"
          }
        />
        <Stat
          label="Unrealised P/L"
          value={
            unrealised != null
              ? formatAmount(unrealised.toString(), inst.currency)
              : "—"
          }
          sub={
            unrealisedPct != null ? `${unrealisedPct.toFixed(2)}% on cost` : ""
          }
          accent={
            unrealised == null
              ? "neutral"
              : unrealised < 0
                ? "neg"
                : unrealised > 0
                  ? "pos"
                  : "neutral"
          }
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

function positionMarketValue(
  position: InstrumentDetail["positions"][number],
  detail: InstrumentDetail
): number | null {
  if (position.marketValue != null) {
    return Number(position.marketValue);
  }
  if (
    !detail.lastQuote ||
    detail.lastQuote.currency !== detail.instrument.currency
  ) {
    return null;
  }
  return Number(position.quantity || 0) * Number(detail.lastQuote.price || 0);
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
      <div className="text-fg-faint text-[11px] font-medium tracking-wide uppercase">
        {label}
      </div>
      <div
        className={
          (mono ? "tabular-nums " : "") +
          (bold ? "text-[15px] font-medium capitalize" : "text-[15px]") +
          (accent === "pos"
            ? "text-emerald-500"
            : accent === "neg"
              ? "text-rose-500"
              : "text-fg")
        }
      >
        {value}
      </div>
      {sub ? <div className="text-fg-muted text-[11px]">{sub}</div> : null}
    </div>
  );
}

function HoldingsOverTime({
  detail,
  reportCurrency,
  onReportCurrencyChange,
}: {
  detail: InstrumentDetail;
  reportCurrency: string;
  onReportCurrencyChange: (value: string) => void;
}) {
  // Build series: for every trade boundary we already have qty; for price-only
  // points we use price * qty when both are available.
  const points = detail.history
    .map((p) => ({
      date: new Date(p.date).getTime(),
      qty: Number(p.quantity || 0),
      value: p.value ? Number(p.value) : null,
      price: p.price ? Number(p.price) : null,
      currency: p.currency,
    }))
    .filter((p) => Number.isFinite(p.date))
    .sort((a, b) => a.date - b.date);
  if (points.length < 2) {
    return null;
  }
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-3">
        <CardTitle>Holding over time</CardTitle>
        <select
          className="border-border bg-surface rounded-[8px] border px-2 py-1 text-[12px]"
          value={reportCurrency}
          onChange={(e) => onReportCurrencyChange(e.target.value)}
        >
          {Array.from(
            new Set([
              detail.reportCurrency,
              detail.instrument.currency,
              "CHF",
              "USD",
              "EUR",
              "GBP",
            ])
          ).map((ccy) => (
            <option key={ccy} value={ccy}>
              {ccy}
            </option>
          ))}
        </select>
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
              unit={` ${detail.reportCurrency}`}
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
          <p className="text-fg-muted px-4 py-6 text-[13px]">
            No trades recorded.
          </p>
        ) : (
          <table className="w-full text-[13px]">
            <thead className="text-fg-muted text-[11px] tracking-wide uppercase">
              <tr className="border-border border-b">
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
                  className="border-border border-b last:border-b-0"
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
                    <QuantityText value={t.quantity} />
                  </td>
                  <td className="px-2 py-2 text-right tabular-nums">
                    {formatAmount(t.price, t.currency)}
                  </td>
                  <td className="text-fg-muted px-2 py-2 text-right tabular-nums">
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
          <p className="text-fg-muted px-4 py-6 text-[13px]">
            No dividend events recorded.
          </p>
        ) : (
          <table className="w-full text-[13px]">
            <thead className="text-fg-muted text-[11px] tracking-wide uppercase">
              <tr className="border-border border-b">
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
                  className="border-border border-b last:border-b-0"
                >
                  <td className="px-4 py-2">{formatDate(d.payDate)}</td>
                  <td className="px-2 py-2 text-right tabular-nums">
                    {formatAmount(d.amountPerUnit, d.currency)}
                  </td>
                  <td className="px-2 py-2 text-right text-emerald-500 tabular-nums">
                    {formatAmount(d.totalAmount, d.currency)}
                  </td>
                  <td className="text-fg-muted px-2 py-2 text-right tabular-nums">
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
