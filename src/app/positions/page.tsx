"use client";

import { useEffect, useState, useCallback, useMemo } from "react";
import Link from "next/link";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { AccountFilter } from "@/components/ui/account-filter";
import { LoadingSpinner } from "@/components/ui/loading";
import { formatMoney, formatPercent, formatQuantity, pnlColor, cn } from "@/lib/utils";
import { usePortfolioCache } from "@/lib/cache-context";
import {
  Search,
  ChevronDown,
  ChevronUp,
  ChevronsUpDown,
  TrendingUp,
  TrendingDown,
  Minus,
} from "lucide-react";

interface Position {
  ticker: string;
  quantity: number;
  avgCostBasis: number;
  totalInvested: number;
  totalSold: number;
  realizedPnL: number;
  currency: string;
  accountId: string;
  broker: string;
  lastTransactionDate: string;
  dividendsReceived: number;
  commissionsTotal: number;
}

type StatusTab = "all" | "open" | "closed";
type SortKey =
  | "ticker"
  | "quantity"
  | "totalInvested"
  | "totalSold"
  | "realizedPnL"
  | "dividendsReceived"
  | "commissionsTotal"
  | "broker"
  | "lastTransactionDate";
type SortDir = "asc" | "desc";

function TrendIcon({ value }: { value: number }) {
  if (value > 0) return <TrendingUp size={14} className="text-green" />;
  if (value < 0) return <TrendingDown size={14} className="text-red" />;
  return <Minus size={14} className="text-text-tertiary" />;
}

export default function PositionsPage() {
  const cache = usePortfolioCache();
  const [positions, setPositions] = useState<Position[]>([]);
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState<StatusTab>("all");
  const [accountId, setAccountId] = useState<string | undefined>(undefined);
  const [search, setSearch] = useState("");
  const [sortBy, setSortBy] = useState<SortKey>("totalInvested");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  const fetchPositions = useCallback(async (s: StatusTab, accId?: string) => {
    const key = `positions|${s}|${accId ?? "ALL"}`;
    const cached = cache.get<Position[]>(key);
    if (cached) {
      setPositions(cached);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const params = new URLSearchParams({ view: "positions", status: s });
      if (accId) params.set("accountId", accId);
      const res = await fetch(`/api/portfolio?${params}`);
      if (res.ok) {
        const data: Position[] = await res.json();
        cache.set(key, data);
        setPositions(data);
      }
    } catch { /* */ }
    setLoading(false);
  }, [cache]);

  useEffect(() => {
    fetchPositions(status, accountId);
  }, [status, accountId, fetchPositions]);

  function toggleSort(key: SortKey) {
    if (sortBy === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
      return;
    }
    setSortBy(key);
    setSortDir("desc");
  }

  const filtered = useMemo(() => {
    let arr = positions;
    if (search) {
      const q = search.toUpperCase();
      arr = arr.filter((p) => p.ticker.includes(q));
    }
    arr = [...arr].sort((a, b) => {
      let av: string | number = 0;
      let bv: string | number = 0;
      switch (sortBy) {
        case "ticker": av = a.ticker; bv = b.ticker; break;
        case "quantity": av = a.quantity; bv = b.quantity; break;
        case "totalInvested": av = a.totalInvested; bv = b.totalInvested; break;
        case "totalSold": av = a.totalSold; bv = b.totalSold; break;
        case "realizedPnL": av = a.realizedPnL; bv = b.realizedPnL; break;
        case "dividendsReceived": av = a.dividendsReceived; bv = b.dividendsReceived; break;
        case "commissionsTotal": av = a.commissionsTotal; bv = b.commissionsTotal; break;
        case "broker": av = a.broker; bv = b.broker; break;
        case "lastTransactionDate": av = a.lastTransactionDate; bv = b.lastTransactionDate; break;
      }
      let cmp = 0;
      if (typeof av === "string" && typeof bv === "string") cmp = av.localeCompare(bv);
      else cmp = Number(av) - Number(bv);
      return sortDir === "asc" ? cmp : -cmp;
    });
    return arr;
  }, [positions, search, sortBy, sortDir]);

  function header(key: SortKey, label: string, className: string) {
    const active = sortBy === key;
    return (
      <th className={className}>
        <button
          onClick={() => toggleSort(key)}
          className="inline-flex items-center gap-1 hover:text-text-secondary transition-colors"
          title={`Sort by ${label}`}
        >
          <span>{label}</span>
          {!active ? (
            <ChevronsUpDown size={12} className="text-text-tertiary/70" />
          ) : sortDir === "asc" ? (
            <ChevronUp size={12} className="text-accent" />
          ) : (
            <ChevronDown size={12} className="text-accent" />
          )}
        </button>
      </th>
    );
  }

  const openCount = positions.filter((p) => p.quantity > 0.00001).length;
  const closedCount = positions.filter((p) => p.quantity <= 0.00001).length;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h2 className="text-2xl font-semibold tracking-tight">All Positions</h2>
          <p className="text-sm text-text-tertiary mt-0.5">
            Lifetime view of all open and closed positions
          </p>
        </div>
        <AccountFilter value={accountId} onChange={setAccountId} />
      </div>

      {/* Controls row */}
      <div className="flex flex-wrap items-center gap-3">
        {/* Status tabs */}
        <div className="flex items-center bg-bg-tertiary rounded-lg p-0.5 border border-border-subtle">
          {(["all", "open", "closed"] as StatusTab[]).map((s) => (
            <button
              key={s}
              onClick={() => setStatus(s)}
              className={cn(
                "px-3 py-1.5 text-xs font-mono font-medium rounded-md transition-all capitalize",
                status === s
                  ? "bg-accent text-bg-primary"
                  : "text-text-tertiary hover:text-text-secondary"
              )}
            >
              {s}
              <span className="ml-1.5 text-[10px] opacity-70">
                {s === "all"
                  ? positions.length
                  : s === "open"
                    ? openCount
                    : closedCount}
              </span>
            </button>
          ))}
        </div>

        {/* Search */}
        <div className="relative w-full sm:flex-1 sm:min-w-[200px] sm:max-w-xs">
          <Search
            size={14}
            className="absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary"
          />
          <input
            type="text"
            placeholder="Search ticker..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full pl-9 pr-3 py-2 bg-bg-tertiary border border-border-subtle rounded-lg text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent font-mono"
          />
        </div>

        <span className="text-xs font-mono text-text-tertiary ml-auto">
          {filtered.length} positions
        </span>
      </div>

      <Card>
        <div className="overflow-x-auto">
          {loading ? (
            <div className="py-20">
              <LoadingSpinner />
            </div>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-[11px] text-text-tertiary uppercase tracking-wider font-mono border-b border-border-subtle">
                  {header("ticker", "Ticker", "px-4 py-3 font-medium")}
                  <th className="px-4 py-3 font-medium">Status</th>
                  {header("quantity", "Qty", "px-4 py-3 font-medium text-right")}
                  {header("totalInvested", "Invested", "px-4 py-3 font-medium text-right")}
                  {header("totalSold", "Sold", "px-4 py-3 font-medium text-right")}
                  {header("realizedPnL", "Realized P&L", "px-4 py-3 font-medium text-right")}
                  {header("dividendsReceived", "Dividends", "px-4 py-3 font-medium text-right")}
                  {header("commissionsTotal", "Fees", "px-4 py-3 font-medium text-right")}
                  {header("broker", "Broker", "px-4 py-3 font-medium")}
                  {header("lastTransactionDate", "Last Trade", "px-4 py-3 font-medium")}
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {filtered.map((p, i) => {
                  const isOpen = p.quantity > 0.00001;
                  return (
                    <tr
                      key={`${p.ticker}-${p.accountId}-${i}`}
                      className="hover:bg-bg-hover transition-colors"
                    >
                      <td className="px-4 py-2.5">
                        <Link
                          href={`/ticker/${encodeURIComponent(p.ticker)}`}
                          className="font-mono font-semibold text-accent hover:underline"
                        >
                          {p.ticker}
                        </Link>
                      </td>
                      <td className="px-4 py-2.5">
                        <span
                          className={cn(
                            "inline-block px-2 py-0.5 rounded text-[10px] font-mono font-medium uppercase",
                            isOpen
                              ? "bg-green-dim text-green"
                              : "bg-bg-tertiary text-text-tertiary"
                          )}
                        >
                          {isOpen ? "Open" : "Closed"}
                        </span>
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono text-text-secondary">
                        {isOpen ? formatQuantity(p.quantity) : "--"}
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono text-text-secondary">
                        {formatMoney(p.totalInvested, p.currency)}
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono text-text-secondary">
                        {p.totalSold > 0
                          ? formatMoney(p.totalSold, p.currency)
                          : "--"}
                      </td>
                      <td className="px-4 py-2.5 text-right">
                        <div className="flex items-center justify-end gap-1.5">
                          <TrendIcon value={p.realizedPnL} />
                          <span className={cn("font-mono", pnlColor(p.realizedPnL))}>
                            {formatMoney(Math.abs(p.realizedPnL), p.currency)}
                          </span>
                        </div>
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono text-yellow">
                        {p.dividendsReceived > 0
                          ? formatMoney(p.dividendsReceived, p.currency)
                          : "--"}
                      </td>
                      <td className="px-4 py-2.5 text-right font-mono text-red text-xs">
                        {p.commissionsTotal > 0
                          ? formatMoney(p.commissionsTotal, p.currency)
                          : "--"}
                      </td>
                      <td className="px-4 py-2.5">
                        <span className="text-[10px] font-mono text-text-tertiary uppercase">
                          {p.broker}
                        </span>
                      </td>
                      <td className="px-4 py-2.5 font-mono text-text-tertiary text-xs">
                        {p.lastTransactionDate}
                      </td>
                    </tr>
                  );
                })}
                {filtered.length === 0 && (
                  <tr>
                    <td colSpan={10} className="px-4 py-12 text-center text-text-tertiary font-mono text-sm">
                      No positions found
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          )}
        </div>
      </Card>
    </div>
  );
}
