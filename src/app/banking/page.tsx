"use client";

import { useEffect, useState, useCallback } from "react";
import { Card } from "@/components/ui/card";
import { LoadingSpinner } from "@/components/ui/loading";
import { cn, formatMoney, pnlColor } from "@/lib/utils";
import {
  Search,
  ChevronLeft,
  ChevronRight,
  Filter,
  ChevronUp,
  ChevronDown,
  ChevronsUpDown,
} from "lucide-react";

interface BankingTxRow {
  transaction: {
    id: string;
    date: string;
    completedDate: string | null;
    description: string;
    amount: string;
    commission: string | null;
    currency: string;
    balance: string | null;
    status: string;
    category: string | null;
    merchant: string | null;
    transferType: string | null;
  };
  accountName: string;
  accountType: string;
}

type SortKey = "date" | "amount" | "merchant" | "category" | "description";
type SortDir = "asc" | "desc";

const STATUS_COLORS: Record<string, string> = {
  completed: "bg-green-dim text-green",
  reversed: "bg-red-dim text-red",
  pending: "bg-yellow-dim text-yellow",
};

export default function BankingPage() {
  const [rows, setRows] = useState<BankingTxRow[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(0);
  const [search, setSearch] = useState("");
  const [categoryFilter, setCategoryFilter] = useState("All");
  const [categories, setCategories] = useState<string[]>([]);
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

  const fetchData = useCallback(async () => {
    setLoading(true);
    const params = new URLSearchParams({
      limit: limit.toString(),
      offset: (page * limit).toString(),
      sortBy,
      sortDir,
    });
    if (search) params.set("search", search);
    if (categoryFilter !== "All") params.set("category", categoryFilter);

    const res = await fetch(`/api/banking/transactions?${params}`);
    const json = await res.json();
    setRows(json.data);
    setTotal(json.total);

    // Extract unique categories from first large fetch for filter
    if (categories.length === 0 && json.data.length > 0) {
      const cats = new Set<string>();
      json.data.forEach((r: BankingTxRow) => {
        if (r.transaction.category) cats.add(r.transaction.category);
      });
      // Fetch all categories from a separate call
      const catRes = await fetch("/api/banking/transactions?limit=1&offset=0");
      const catJson = await catRes.json();
      if (catJson.total > 0) {
        // Fetch distinct categories
        const allRes = await fetch(`/api/banking/transactions?limit=${Math.min(catJson.total, 5000)}&offset=0`);
        const allJson = await allRes.json();
        const allCats = new Set<string>();
        allJson.data.forEach((r: BankingTxRow) => {
          if (r.transaction.category) allCats.add(r.transaction.category);
        });
        setCategories([...allCats].sort());
      }
    }

    setLoading(false);
  }, [page, search, categoryFilter, sortBy, sortDir]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  useEffect(() => {
    setPage(0);
  }, [search, categoryFilter, sortBy, sortDir]);

  const totalPages = Math.ceil(total / limit);

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold tracking-tight">Banking</h2>
        <p className="text-sm text-text-tertiary mt-0.5">
          All banking transactions across accounts
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative w-full sm:flex-1 sm:min-w-[200px] sm:max-w-xs">
          <Search
            size={14}
            className="absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary"
          />
          <input
            type="text"
            placeholder="Search description or merchant..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full pl-9 pr-3 py-2 bg-bg-tertiary border border-border-subtle rounded-lg text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent font-mono"
          />
        </div>

        <div className="flex items-center gap-1.5">
          <Filter size={14} className="text-text-tertiary" />
          <select
            value={categoryFilter}
            onChange={(e) => setCategoryFilter(e.target.value)}
            className="bg-bg-tertiary border border-border-subtle rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent font-mono appearance-none cursor-pointer max-w-full sm:max-w-[250px]"
          >
            <option value="All">All Categories</option>
            {categories.map((c) => (
              <option key={c} value={c}>
                {c}
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
                  {header("description", "Description", "px-4 py-3 font-medium")}
                  {header("merchant", "Merchant", "px-4 py-3 font-medium")}
                  {header("amount", "Amount", "px-4 py-3 font-medium text-right")}
                  {header("category", "Category", "px-4 py-3 font-medium")}
                  <th className="px-4 py-3 font-medium">Account</th>
                  <th className="px-4 py-3 font-medium">Status</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {rows.map((r) => {
                  const tx = r.transaction;
                  const d = new Date(tx.date);
                  const amount = parseFloat(tx.amount);
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
                      <td className="px-4 py-2.5 text-text-secondary max-w-[300px] truncate" title={tx.description}>
                        {tx.description}
                      </td>
                      <td className="px-4 py-2.5 font-mono text-text-secondary text-xs">
                        {tx.merchant || (
                          <span className="text-text-tertiary">--</span>
                        )}
                      </td>
                      <td className={cn(
                        "px-4 py-2.5 text-right font-mono font-medium whitespace-nowrap",
                        pnlColor(amount)
                      )}>
                        {formatMoney(amount, tx.currency)}
                      </td>
                      <td className="px-4 py-2.5">
                        {tx.category ? (
                          <span className="inline-block px-2 py-0.5 rounded text-[10px] font-mono bg-bg-tertiary text-text-secondary truncate max-w-[200px]">
                            {tx.category}
                          </span>
                        ) : (
                          <span className="text-text-tertiary text-xs">--</span>
                        )}
                      </td>
                      <td className="px-4 py-2.5">
                        <span className="text-[10px] font-mono text-text-tertiary uppercase">
                          {r.accountName}
                        </span>
                      </td>
                      <td className="px-4 py-2.5">
                        <span
                          className={cn(
                            "inline-block px-2 py-0.5 rounded text-[10px] font-mono font-medium uppercase",
                            STATUS_COLORS[tx.status] || "bg-bg-tertiary text-text-tertiary"
                          )}
                        >
                          {tx.status}
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
