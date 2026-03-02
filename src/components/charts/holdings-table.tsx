"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
import { formatMoney, formatPercent, formatQuantity, pnlColor, cn } from "@/lib/utils";
import {
  TrendingUp,
  TrendingDown,
  Minus,
  ChevronDown,
  ChevronUp,
  ChevronsUpDown,
} from "lucide-react";
import { Tip } from "@/components/ui/tip";

interface Holding {
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
  realizedPnL: number;
  totalInvested: number;
  commissionsTotal: number;
}

type SortKey =
  | "ticker"
  | "quantity"
  | "avgCostBasis"
  | "currentPrice"
  | "currentValue"
  | "unrealizedPnL"
  | "realizedPnL"
  | "totalReturn"
  | "totalReturnPercent";
type SortDir = "asc" | "desc";

function TrendIcon({ value }: { value: number }) {
  if (value > 0) return <TrendingUp size={14} className="text-green" />;
  if (value < 0) return <TrendingDown size={14} className="text-red" />;
  return <Minus size={14} className="text-text-tertiary" />;
}

function SortIcon({
  active,
  dir,
}: {
  active: boolean;
  dir: SortDir;
}) {
  if (!active) return <ChevronsUpDown size={12} className="text-text-tertiary/70" />;
  return dir === "asc" ? (
    <ChevronUp size={12} className="text-accent" />
  ) : (
    <ChevronDown size={12} className="text-accent" />
  );
}

export function HoldingsTable({
  holdings,
  displayCurrency,
}: {
  holdings: Holding[];
  displayCurrency: string;
}) {
  const [sortKey, setSortKey] = useState<SortKey>("currentValue");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  function handleSort(key: SortKey) {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
      return;
    }
    setSortKey(key);
    setSortDir("desc");
  }

  const sortedHoldings = useMemo(() => {
    const arr = [...holdings];
    arr.sort((a, b) => {
      let av: string | number = 0;
      let bv: string | number = 0;

      switch (sortKey) {
        case "ticker":
          av = a.ticker;
          bv = b.ticker;
          break;
        case "quantity":
          av = a.quantity;
          bv = b.quantity;
          break;
        case "avgCostBasis":
          av = a.avgCostBasis;
          bv = b.avgCostBasis;
          break;
        case "currentPrice":
          av = a.currentPrice ?? Number.NEGATIVE_INFINITY;
          bv = b.currentPrice ?? Number.NEGATIVE_INFINITY;
          break;
        case "currentValue":
          av = a.currentValue;
          bv = b.currentValue;
          break;
        case "unrealizedPnL":
          av = a.unrealizedPnL;
          bv = b.unrealizedPnL;
          break;
        case "realizedPnL":
          av = a.realizedPnL;
          bv = b.realizedPnL;
          break;
        case "totalReturn":
          av = a.totalReturn;
          bv = b.totalReturn;
          break;
        case "totalReturnPercent":
          av = a.totalReturnPercent;
          bv = b.totalReturnPercent;
          break;
      }

      let cmp = 0;
      if (typeof av === "string" && typeof bv === "string") {
        cmp = av.localeCompare(bv);
      } else {
        cmp = Number(av) - Number(bv);
      }
      return sortDir === "asc" ? cmp : -cmp;
    });
    return arr;
  }, [holdings, sortKey, sortDir]);

  function sortableHeader(
    key: SortKey,
    label: string,
    className: string,
    tooltip?: string
  ) {
    const active = sortKey === key;
    return (
      <th className={className}>
        <button
          onClick={() => handleSort(key)}
          className="inline-flex items-center gap-1 hover:text-text-secondary transition-colors"
        >
          {tooltip ? (
            <Tip text={tooltip}><span>{label}</span></Tip>
          ) : (
            <span>{label}</span>
          )}
          <SortIcon active={active} dir={sortDir} />
        </button>
      </th>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-[11px] text-text-tertiary uppercase tracking-wider font-mono">
            {sortableHeader("ticker", "Ticker", "px-4 py-3 font-medium")}
            {sortableHeader("quantity", "Qty", "px-4 py-3 font-medium text-right")}
            {sortableHeader("avgCostBasis", "Avg Cost", "px-4 py-3 font-medium text-right", "Average price paid per share")}
            {sortableHeader("currentPrice", "Price", "px-4 py-3 font-medium text-right", "Latest market price")}
            {sortableHeader("currentValue", "Value", "px-4 py-3 font-medium text-right", "Qty x current price, in display currency")}
            {sortableHeader("unrealizedPnL", "Unrealized", "px-4 py-3 font-medium text-right", "Gain/loss on shares you still hold")}
            {sortableHeader("realizedPnL", "Realized", "px-4 py-3 font-medium text-right", "Gain/loss from shares already sold")}
            {sortableHeader("totalReturn", "Lifetime", "px-4 py-3 font-medium text-right", "Unrealized + realized + dividends - fees. Total all-time P&L")}
            {sortableHeader("totalReturnPercent", "Return %", "px-4 py-3 font-medium text-right", "Lifetime return as % of total invested")}
          </tr>
        </thead>
        <tbody className="divide-y divide-border-subtle">
          {sortedHoldings.map((h, i) => (
            <tr
              key={`${h.ticker}-${i}`}
              className="hover:bg-bg-hover transition-colors group"
            >
              <td className="px-4 py-3">
                <Link
                  href={`/ticker/${encodeURIComponent(h.ticker)}`}
                  className="font-mono font-semibold text-accent hover:underline"
                >
                  {h.ticker}
                </Link>
              </td>
              <td className="px-4 py-3 text-right font-mono text-text-secondary">
                {formatQuantity(h.quantity)}
              </td>
              <td className="px-4 py-3 text-right font-mono text-text-secondary">
                {formatMoney(h.avgCostBasis, h.currency)}
              </td>
              <td className="px-4 py-3 text-right font-mono text-text-primary">
                {h.currentPrice
                  ? formatMoney(h.currentPrice, h.currency)
                  : "N/A"}
              </td>
              <td className="px-4 py-3 text-right font-mono font-medium text-text-primary">
                {formatMoney(h.currentValue, displayCurrency)}
              </td>
              <td className="px-4 py-3 text-right">
                <div className="flex items-center justify-end gap-1.5">
                  <TrendIcon value={h.unrealizedPnL} />
                  <span className={cn("font-mono", pnlColor(h.unrealizedPnL))}>
                    {formatMoney(Math.abs(h.unrealizedPnL), displayCurrency)}
                  </span>
                </div>
              </td>
              <td className="px-4 py-3 text-right">
                <span className={cn("font-mono", pnlColor(h.realizedPnL))}>
                  {h.realizedPnL !== 0
                    ? formatMoney(h.realizedPnL, displayCurrency)
                    : "--"}
                </span>
              </td>
              <td className="px-4 py-3 text-right">
                <span className={cn("font-mono font-medium", pnlColor(h.totalReturn))}>
                  {formatMoney(h.totalReturn, displayCurrency)}
                </span>
              </td>
              <td className="px-4 py-3 text-right">
                <span
                  className={cn(
                    "inline-block px-2 py-0.5 rounded text-xs font-mono font-medium",
                    h.totalReturnPercent > 0 && "bg-green-dim text-green",
                    h.totalReturnPercent < 0 && "bg-red-dim text-red",
                    h.totalReturnPercent === 0 && "bg-bg-tertiary text-text-tertiary"
                  )}
                >
                  {formatPercent(h.totalReturnPercent)}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
