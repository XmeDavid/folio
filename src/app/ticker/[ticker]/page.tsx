"use client";

import { useEffect, useState, useCallback, useMemo } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import {
  CurrencyToggle,
  type DisplayCurrency,
} from "@/components/ui/currency-toggle";
import { LoadingSpinner } from "@/components/ui/loading";
import { TimeSeriesChart } from "@/components/charts/time-series-chart";
import { formatMoney, formatPercent, pnlColor, cn } from "@/lib/utils";
import { usePortfolioCache } from "@/lib/cache-context";
import {
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  ChevronsUpDown,
} from "lucide-react";
import { Tip } from "@/components/ui/tip";

interface TickerPoint {
  date: string;
  quantity: number;
  marketValue: number;
  costBasis: number;
  unrealizedPnL: number;
  realizedPnL: number;
  dividends: number;
  fees: number;
  totalReturn: number;
  priceOnlyReturn: number;
}

interface TransactionRow {
  transaction: {
    id: string;
    date: string;
    ticker: string | null;
    type: string;
    quantity: string | null;
    unitPrice: string | null;
    totalAmount: string;
    currency: string;
    commission: string | null;
    fxRateOriginal: string | null;
  };
  accountName: string;
  broker: string;
}

type Metric = "total" | "priceOnly";
type TxSortKey = "date" | "type" | "quantity" | "unitPrice" | "totalAmount" | "broker";
type SortDir = "asc" | "desc";

const TYPE_COLORS: Record<string, string> = {
  "BUY - MARKET": "bg-green-dim text-green",
  "BUY - LIMIT": "bg-green-dim text-green",
  BUY: "bg-green-dim text-green",
  "SELL - MARKET": "bg-red-dim text-red",
  "SELL - LIMIT": "bg-red-dim text-red",
  DIVIDEND: "bg-yellow-dim text-yellow",
  "STOCK SPLIT": "bg-bg-tertiary text-text-secondary",
  "MERGER - STOCK": "bg-bg-tertiary text-text-secondary",
  "POSITION CLOSURE": "bg-bg-tertiary text-text-secondary",
  "FX BUY": "bg-accent-glow text-accent",
  "FX SELL": "bg-accent-glow text-accent",
};

export default function TickerPage() {
  const params = useParams();
  const ticker = decodeURIComponent(params.ticker as string);
  const cache = usePortfolioCache();

  const [currency, setCurrency] = useState<DisplayCurrency>("CHF");
  const [metric, setMetric] = useState<Metric>("total");
  const [series, setSeries] = useState<TickerPoint[]>([]);
  const [txns, setTxns] = useState<TransactionRow[]>([]);
  const [seriesLoading, setSeriesLoading] = useState(true);
  const [txnLoading, setTxnLoading] = useState(true);
  const [txSortBy, setTxSortBy] = useState<TxSortKey>("date");
  const [txSortDir, setTxSortDir] = useState<SortDir>("desc");

  const fetchSeries = useCallback(
    async (cur: DisplayCurrency) => {
      const key = `ticker-series|${ticker}|${cur}`;
      const cached = cache.get<TickerPoint[]>(key);
      if (cached) {
        setSeries(cached);
        setSeriesLoading(false);
        return;
      }
      setSeriesLoading(true);
      try {
        const p = new URLSearchParams({
          view: "ticker",
          ticker,
          currency: cur,
        });
        const res = await fetch(`/api/portfolio?${p}`);
        if (res.ok) {
          const data: TickerPoint[] = await res.json();
          cache.set(key, data);
          setSeries(data);
        }
      } catch { /* */ }
      setSeriesLoading(false);
    },
    [ticker, cache]
  );

  const fetchTxns = useCallback(async () => {
    const key = `ticker-txns|${ticker}`;
    const cached = cache.get<TransactionRow[]>(key);
    if (cached) {
      setTxns(cached);
      setTxnLoading(false);
      return;
    }
    setTxnLoading(true);
    try {
      const p = new URLSearchParams({ ticker, limit: "500" });
      const res = await fetch(`/api/transactions?${p}`);
      if (res.ok) {
        const json = await res.json();
        cache.set(key, json.data);
        setTxns(json.data);
      }
    } catch { /* */ }
    setTxnLoading(false);
  }, [ticker, cache]);

  useEffect(() => {
    fetchSeries(currency);
    fetchTxns();
  }, [currency, fetchSeries, fetchTxns]);

  function toggleTxSort(key: TxSortKey) {
    if (txSortBy === key) {
      setTxSortDir((d) => (d === "asc" ? "desc" : "asc"));
      return;
    }
    setTxSortBy(key);
    setTxSortDir(key === "date" ? "desc" : "asc");
  }

  const sortedTxns = useMemo(() => {
    const arr = [...txns];
    arr.sort((a, b) => {
      const ta = a.transaction;
      const tb = b.transaction;
      let cmp = 0;
      switch (txSortBy) {
        case "date":
          cmp = new Date(ta.date).getTime() - new Date(tb.date).getTime();
          break;
        case "type":
          cmp = ta.type.localeCompare(tb.type);
          break;
        case "quantity":
          cmp = parseFloat(ta.quantity || "0") - parseFloat(tb.quantity || "0");
          break;
        case "unitPrice":
          cmp = parseFloat(ta.unitPrice || "0") - parseFloat(tb.unitPrice || "0");
          break;
        case "totalAmount":
          cmp = parseFloat(ta.totalAmount || "0") - parseFloat(tb.totalAmount || "0");
          break;
        case "broker":
          cmp = a.broker.localeCompare(b.broker);
          break;
      }
      return txSortDir === "asc" ? cmp : -cmp;
    });
    return arr;
  }, [txns, txSortBy, txSortDir]);

  function txHeader(key: TxSortKey, label: string, className: string) {
    const active = txSortBy === key;
    return (
      <th className={className}>
        <button
          onClick={() => toggleTxSort(key)}
          className="inline-flex items-center gap-1 hover:text-text-secondary transition-colors"
          title={`Sort by ${label}`}
        >
          <span>{label}</span>
          {!active ? (
            <ChevronsUpDown size={12} className="text-text-tertiary/70" />
          ) : txSortDir === "asc" ? (
            <ChevronUp size={12} className="text-accent" />
          ) : (
            <ChevronDown size={12} className="text-accent" />
          )}
        </button>
      </th>
    );
  }

  const latest = series.length > 0 ? series[series.length - 1] : null;

  const chartSeries =
    metric === "total"
      ? [
          { key: "marketValue", color: "#6c9cff", name: "Market Value" },
          { key: "costBasis", color: "#5c6278", name: "Cost Basis" },
          { key: "totalReturn", color: "#3dd68c", name: "Total Return" },
        ]
      : [
          { key: "marketValue", color: "#6c9cff", name: "Market Value" },
          { key: "costBasis", color: "#5c6278", name: "Cost Basis" },
          { key: "priceOnlyReturn", color: "#ffc145", name: "Price Return" },
        ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Link
            href="/"
            className="p-2 rounded-lg bg-bg-tertiary border border-border-subtle text-text-tertiary hover:text-text-primary hover:bg-bg-hover transition-colors"
          >
            <ArrowLeft size={16} />
          </Link>
          <div>
            <h2 className="text-2xl font-semibold tracking-tight font-mono">
              {ticker}
            </h2>
            <p className="text-sm text-text-tertiary mt-0.5">
              Position details and history
            </p>
          </div>
        </div>
        <CurrencyToggle value={currency} onChange={setCurrency} />
      </div>

      {/* Summary cards */}
      {latest && (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <Card>
            <CardContent>
              <p className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1">
                <Tip text="Qty x current price, converted to display currency">Market Value</Tip>
              </p>
              <p className="text-xl font-mono font-semibold">
                {formatMoney(latest.marketValue, currency)}
              </p>
            </CardContent>
          </Card>
          <Card>
            <CardContent>
              <p className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1">
                Quantity
              </p>
              <p className="text-xl font-mono font-semibold">
                {latest.quantity}
              </p>
            </CardContent>
          </Card>
          <Card>
            <CardContent>
              <p className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1">
                <Tip text="Unrealized + realized + dividends - fees. All-time P&L for this position">Total Return</Tip>
              </p>
              <p className={cn("text-xl font-mono font-semibold", pnlColor(latest.totalReturn))}>
                {formatMoney(latest.totalReturn, currency)}
              </p>
            </CardContent>
          </Card>
          <Card>
            <CardContent>
              <p className="text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1">
                <Tip text="Total dividends received from this position">Dividends</Tip>
              </p>
              <p className="text-xl font-mono font-semibold text-yellow">
                {formatMoney(latest.dividends, currency)}
              </p>
            </CardContent>
          </Card>
        </div>
      )}

      {/* Chart */}
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle>{ticker} Performance</CardTitle>
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
          </div>
        </CardHeader>
        <CardContent>
          {seriesLoading ? (
            <div className="h-[320px] flex items-center justify-center">
              <LoadingSpinner />
            </div>
          ) : (
            <TimeSeriesChart
              data={series}
              series={chartSeries}
              currency={currency}
            />
          )}
        </CardContent>
      </Card>

      {/* Transactions */}
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle>Transactions</CardTitle>
            <span className="text-xs font-mono text-text-tertiary">
              {txns.length} transactions
            </span>
          </div>
        </CardHeader>
        <div className="overflow-x-auto">
          {txnLoading ? (
            <div className="py-12">
              <LoadingSpinner />
            </div>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-[11px] text-text-tertiary uppercase tracking-wider font-mono border-b border-border-subtle">
                  {txHeader("date", "Date", "px-4 py-3 font-medium")}
                  {txHeader("type", "Type", "px-4 py-3 font-medium")}
                  {txHeader("quantity", "Qty", "px-4 py-3 font-medium text-right")}
                  {txHeader("unitPrice", "Price", "px-4 py-3 font-medium text-right")}
                  {txHeader("totalAmount", "Total", "px-4 py-3 font-medium text-right")}
                  {txHeader("broker", "Broker", "px-4 py-3 font-medium")}
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {sortedTxns.map((r) => {
                  const tx = r.transaction;
                  const d = new Date(tx.date);
                  return (
                    <tr
                      key={tx.id}
                      className="hover:bg-bg-hover transition-colors"
                    >
                      <td className="px-4 py-2.5 font-mono text-text-secondary text-xs whitespace-nowrap">
                        {d.toLocaleDateString("en-GB", {
                          day: "2-digit",
                          month: "short",
                          year: "numeric",
                        })}
                      </td>
                      <td className="px-4 py-2.5">
                        <span
                          className={cn(
                            "inline-block px-2 py-0.5 rounded text-[10px] font-mono font-medium uppercase",
                            TYPE_COLORS[tx.type] || "bg-bg-tertiary text-text-tertiary"
                          )}
                        >
                          {tx.type}
                        </span>
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono text-text-secondary">
                        {tx.quantity
                          ? parseFloat(tx.quantity).toFixed(
                              parseFloat(tx.quantity) < 1 ? 6 : 2
                            )
                          : "--"}
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono text-text-secondary">
                        {tx.unitPrice
                          ? `${tx.currency} ${parseFloat(tx.unitPrice).toFixed(2)}`
                          : "--"}
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono font-medium text-text-primary">
                        {tx.currency}{" "}
                        {parseFloat(tx.totalAmount).toFixed(2)}
                      </td>
                      <td className="px-4 py-2.5">
                        <span className="text-[10px] font-mono text-text-tertiary uppercase">
                          {r.broker}
                        </span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      </Card>
    </div>
  );
}
