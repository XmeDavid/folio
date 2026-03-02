"use client";

import { useEffect, useState, useCallback } from "react";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { LoadingSpinner } from "@/components/ui/loading";
import { cn } from "@/lib/utils";
import Link from "next/link";
import {
  Search,
  ChevronLeft,
  ChevronRight,
  Filter,
  ChevronUp,
  ChevronDown,
  ChevronsUpDown,
} from "lucide-react";

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

const TYPE_COLORS: Record<string, string> = {
  "BUY - MARKET": "bg-green-dim text-green",
  "BUY - LIMIT": "bg-green-dim text-green",
  BUY: "bg-green-dim text-green",
  "SELL - MARKET": "bg-red-dim text-red",
  "SELL - LIMIT": "bg-red-dim text-red",
  DIVIDEND: "bg-yellow-dim text-yellow",
  "DIVIDEND TAX (CORRECTION)": "bg-yellow-dim text-yellow",
  "CASH TOP-UP": "bg-accent-glow text-accent",
  "CASH WITHDRAWAL": "bg-bg-tertiary text-text-tertiary",
  "CUSTODY FEE": "bg-red-dim text-red",
  "ROBO MANAGEMENT FEE": "bg-red-dim text-red",
  "STOCK SPLIT": "bg-bg-tertiary text-text-secondary",
  "MERGER - STOCK": "bg-bg-tertiary text-text-secondary",
  "POSITION CLOSURE": "bg-bg-tertiary text-text-secondary",
  "FX BUY": "bg-accent-glow text-accent",
  "FX SELL": "bg-accent-glow text-accent",
};

const TYPES = [
  "All",
  "BUY - MARKET",
  "BUY - LIMIT",
  "BUY",
  "SELL - MARKET",
  "SELL - LIMIT",
  "DIVIDEND",
  "CASH TOP-UP",
  "CASH WITHDRAWAL",
  "CUSTODY FEE",
  "ROBO MANAGEMENT FEE",
  "STOCK SPLIT",
  "MERGER - STOCK",
  "FX BUY",
  "FX SELL",
];

type SortKey =
  | "date"
  | "ticker"
  | "type"
  | "quantity"
  | "unitPrice"
  | "totalAmount"
  | "fxRateOriginal"
  | "broker";
type SortDir = "asc" | "desc";

export default function TransactionsPage() {
  const [rows, setRows] = useState<TransactionRow[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(0);
  const [search, setSearch] = useState("");
  const [typeFilter, setTypeFilter] = useState("All");
  const [sortBy, setSortBy] = useState<SortKey>("date");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  const limit = 50;

  function toggleSort(key: SortKey) {
    if (sortBy === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
      return;
    }
    setSortBy(key);
    setSortDir(key === "date" ? "desc" : "asc");
  }

  function header(
    key: SortKey,
    label: string,
    className: string
  ) {
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

  const fetchData = useCallback(async () => {
    setLoading(true);
    const params = new URLSearchParams({
      limit: limit.toString(),
      offset: (page * limit).toString(),
      sortBy,
      sortDir,
    });
    if (search) params.set("ticker", search.toUpperCase());
    if (typeFilter !== "All") params.set("type", typeFilter);

    const res = await fetch(`/api/transactions?${params}`);
    const json = await res.json();
    setRows(json.data);
    setTotal(json.total);
    setLoading(false);
  }, [page, search, typeFilter, sortBy, sortDir]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  useEffect(() => {
    setPage(0);
  }, [search, typeFilter, sortBy, sortDir]);

  const totalPages = Math.ceil(total / limit);

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold tracking-tight">Transactions</h2>
        <p className="text-sm text-text-tertiary mt-0.5">
          All transactions across brokers
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[200px] max-w-xs">
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

        <div className="flex items-center gap-1.5">
          <Filter size={14} className="text-text-tertiary" />
          <select
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value)}
            className="bg-bg-tertiary border border-border-subtle rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent font-mono appearance-none cursor-pointer"
          >
            {TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </div>

        <span className="text-xs font-mono text-text-tertiary ml-auto">
          {total.toLocaleString()} transactions
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
                  {header("date", "Date", "px-4 py-3 font-medium")}
                  {header("ticker", "Ticker", "px-4 py-3 font-medium")}
                  {header("type", "Type", "px-4 py-3 font-medium")}
                  {header("quantity", "Qty", "px-4 py-3 font-medium text-right")}
                  {header("unitPrice", "Price", "px-4 py-3 font-medium text-right")}
                  {header("totalAmount", "Total", "px-4 py-3 font-medium text-right")}
                  {header("fxRateOriginal", "FX", "px-4 py-3 font-medium text-right")}
                  {header("broker", "Broker", "px-4 py-3 font-medium")}
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {rows.map((r) => {
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
                        <span className="text-text-tertiary ml-1.5">
                          {d.toLocaleTimeString("en-GB", {
                            hour: "2-digit",
                            minute: "2-digit",
                          })}
                        </span>
                      </td>
                      <td className="px-4 py-2.5">
                        {tx.ticker ? (
                          <Link
                            href={`/ticker/${encodeURIComponent(tx.ticker)}`}
                            className="font-mono font-semibold text-accent hover:underline"
                          >
                            {tx.ticker}
                          </Link>
                        ) : (
                          <span className="text-text-tertiary">--</span>
                        )}
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
                      <td className="px-4 py-2.5 text-right font-mono text-text-tertiary text-xs">
                        {tx.fxRateOriginal
                          ? parseFloat(tx.fxRateOriginal).toFixed(4)
                          : "--"}
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

        {totalPages > 1 && (
          <div className="flex items-center justify-between px-4 py-3 border-t border-border-subtle">
            <button
              onClick={() => setPage((p) => Math.max(0, p - 1))}
              disabled={page === 0}
              className="flex items-center gap-1 px-3 py-1.5 text-xs font-mono text-text-secondary hover:text-text-primary disabled:opacity-30 disabled:cursor-not-allowed"
            >
              <ChevronLeft size={14} /> Prev
            </button>
            <span className="text-xs font-mono text-text-tertiary">
              Page {page + 1} of {totalPages}
            </span>
            <button
              onClick={() => setPage((p) => Math.min(totalPages - 1, p + 1))}
              disabled={page >= totalPages - 1}
              className="flex items-center gap-1 px-3 py-1.5 text-xs font-mono text-text-secondary hover:text-text-primary disabled:opacity-30 disabled:cursor-not-allowed"
            >
              Next <ChevronRight size={14} />
            </button>
          </div>
        )}
      </Card>
    </div>
  );
}
