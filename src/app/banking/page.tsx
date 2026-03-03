"use client";

import { Suspense, useEffect, useState, useCallback, useMemo, useRef } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
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
  ArrowRightLeft,
  ArrowRight,
  X,
  Calendar,
  Check,
  Plus,
  Tag,
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
    tags: string[];
  };
  accountName: string;
  accountType: string;
}

interface TransferRow {
  id: string;
  date: string;
  fromAccountId: string;
  fromAccountName: string;
  toAccountId: string;
  toAccountName: string;
  amount: string;
  currency: string;
  description: string;
}

type SortKey = "date" | "amount" | "merchant" | "category" | "description";
type SortDir = "asc" | "desc";
type DisplayRow =
  | { kind: "transaction"; data: BankingTxRow }
  | { kind: "transfer"; data: TransferRow };

const STATUS_COLORS: Record<string, string> = {
  completed: "bg-green-dim text-green",
  reversed: "bg-red-dim text-red",
  pending: "bg-yellow-dim text-yellow",
};

export default function BankingPage() {
  return (
    <Suspense fallback={<LoadingSpinner />}>
      <BankingContent />
    </Suspense>
  );
}

function BankingContent() {
  const router = useRouter();
  const searchParams = useSearchParams();

  // Read initial filter values from URL
  const [rows, setRows] = useState<BankingTxRow[]>([]);
  const [transfers, setTransfers] = useState<TransferRow[]>([]);
  const [total, setTotal] = useState(0);
  const [transferTotal, setTransferTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(0);

  const [search, setSearch] = useState(searchParams.get("search") || "");
  const [dateFrom, setDateFrom] = useState(searchParams.get("from") || "");
  const [dateTo, setDateTo] = useState(searchParams.get("to") || "");
  const [categoryFilter, setCategoryFilter] = useState(searchParams.get("category") || "");
  const [tagFilter, setTagFilter] = useState(searchParams.get("tag") || "");
  const [merchantFilter, setMerchantFilter] = useState(searchParams.get("merchant") || "");
  const [showTransfers, setShowTransfers] = useState(false);
  const [filtersOpen, setFiltersOpen] = useState(false);

  const [sortBy, setSortBy] = useState<SortKey>("date");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  const [allCategories, setAllCategories] = useState<string[]>([]);
  const [allTags, setAllTags] = useState<string[]>([]);

  const limit = 50;

  // Auto-open filters if URL has filter params
  useEffect(() => {
    if (dateFrom || dateTo || categoryFilter || tagFilter || merchantFilter) {
      setFiltersOpen(true);
    }
  }, []);

  // Fetch categories and tags
  useEffect(() => {
    fetch("/api/categories").then((r) => r.json()).then((j) => setAllCategories(j.data));
    fetch("/api/tags").then((r) => r.json()).then((j) => setAllTags(j.data));
  }, []);

  // Count active filters
  const activeFilterCount = [dateFrom, dateTo, categoryFilter, tagFilter, merchantFilter].filter(Boolean).length;

  // Update URL when filters change
  useEffect(() => {
    const params = new URLSearchParams();
    if (search) params.set("search", search);
    if (dateFrom) params.set("from", dateFrom);
    if (dateTo) params.set("to", dateTo);
    if (categoryFilter) params.set("category", categoryFilter);
    if (tagFilter) params.set("tag", tagFilter);
    if (merchantFilter) params.set("merchant", merchantFilter);
    const qs = params.toString();
    router.replace(`/banking${qs ? `?${qs}` : ""}`, { scroll: false });
  }, [search, dateFrom, dateTo, categoryFilter, tagFilter, merchantFilter]);

  function toggleSort(key: SortKey) {
    if (sortBy === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
      return;
    }
    setSortBy(key);
    setSortDir(key === "date" ? "desc" : "asc");
  }

  function headerCell(key: SortKey, label: string, className: string) {
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
    if (dateFrom) params.set("from", dateFrom);
    if (dateTo) params.set("to", dateTo);
    if (categoryFilter) params.set("category", categoryFilter);
    if (tagFilter) params.set("tag", tagFilter);
    if (merchantFilter) params.set("merchant", merchantFilter);

    const fetches: Promise<any>[] = [
      fetch(`/api/banking/transactions?${params}`).then((r) => r.json()),
    ];

    if (showTransfers) {
      const transferParams = new URLSearchParams({
        limit: limit.toString(),
        offset: (page * limit).toString(),
      });
      if (dateFrom) transferParams.set("from", dateFrom);
      if (dateTo) transferParams.set("to", dateTo);
      fetches.push(
        fetch(`/api/banking/transfers?${transferParams}`).then((r) => r.json())
      );
    }

    const [txJson, transferJson] = await Promise.all(fetches);
    setRows(txJson.data);
    setTotal(txJson.total);

    if (transferJson) {
      setTransfers(transferJson.data);
      setTransferTotal(transferJson.total);
    } else {
      setTransfers([]);
      setTransferTotal(0);
    }

    setLoading(false);
  }, [page, search, dateFrom, dateTo, categoryFilter, tagFilter, merchantFilter, sortBy, sortDir, showTransfers]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  useEffect(() => {
    setPage(0);
  }, [search, dateFrom, dateTo, categoryFilter, tagFilter, merchantFilter, sortBy, sortDir, showTransfers]);

  const displayRows = useMemo<DisplayRow[]>(() => {
    const txRows: DisplayRow[] = rows.map((r) => ({ kind: "transaction", data: r }));
    if (!showTransfers || transfers.length === 0) return txRows;

    const transferRows: DisplayRow[] = transfers.map((t) => ({ kind: "transfer", data: t }));
    const merged = [...txRows, ...transferRows];
    merged.sort((a, b) => {
      const dateA = new Date(a.kind === "transaction" ? a.data.transaction.date : a.data.date).getTime();
      const dateB = new Date(b.kind === "transaction" ? b.data.transaction.date : b.data.date).getTime();
      return sortDir === "desc" ? dateB - dateA : dateA - dateB;
    });
    return merged;
  }, [rows, transfers, showTransfers, sortDir]);

  const totalPages = Math.ceil(total / limit);

  function clearAllFilters() {
    setSearch("");
    setDateFrom("");
    setDateTo("");
    setCategoryFilter("");
    setTagFilter("");
    setMerchantFilter("");
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold tracking-tight">Banking</h2>
        <p className="text-sm text-text-tertiary mt-0.5">
          All banking transactions across accounts
        </p>
      </div>

      {/* Main filter row */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative w-full sm:flex-1 sm:min-w-[200px] sm:max-w-xs">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary" />
          <input
            type="text"
            placeholder="Search description or merchant..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full pl-9 pr-3 py-2 bg-bg-tertiary border border-border-subtle rounded-lg text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent font-mono"
          />
        </div>

        <button
          onClick={() => setFiltersOpen((v) => !v)}
          className={cn(
            "flex items-center gap-1.5 px-3 py-2 rounded-lg text-xs font-mono font-medium border transition-colors",
            filtersOpen || activeFilterCount > 0
              ? "bg-accent-glow border-accent text-accent"
              : "bg-bg-tertiary border-border-subtle text-text-tertiary hover:text-text-secondary"
          )}
        >
          <Filter size={12} />
          Filters
          {activeFilterCount > 0 && (
            <span className="ml-0.5 px-1.5 py-0.5 bg-accent text-bg-primary rounded-full text-[10px] font-bold">
              {activeFilterCount}
            </span>
          )}
        </button>

        <button
          onClick={() => setShowTransfers((v) => !v)}
          className={cn(
            "flex items-center gap-1.5 px-3 py-2 rounded-lg text-xs font-mono font-medium border transition-colors",
            showTransfers
              ? "bg-accent-glow border-accent text-accent"
              : "bg-bg-tertiary border-border-subtle text-text-tertiary hover:text-text-secondary"
          )}
        >
          <ArrowRightLeft size={12} />
          Transfers
        </button>

        <span className="text-xs font-mono text-text-tertiary ml-auto">
          {total.toLocaleString()} transactions
          {showTransfers && transferTotal > 0 && (
            <span className="text-accent ml-1">+ {transferTotal} transfers</span>
          )}
        </span>
      </div>

      {/* Collapsible filter row */}
      {filtersOpen && (
        <div className="flex flex-wrap items-end gap-3 p-3 bg-bg-secondary border border-border-subtle rounded-lg">
          {/* Date range */}
          <div className="flex items-end gap-2">
            <div>
              <label className="block text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-1">From</label>
              <div className="relative">
                <Calendar size={12} className="absolute left-2 top-1/2 -translate-y-1/2 text-text-tertiary" />
                <input
                  type="date"
                  value={dateFrom}
                  onChange={(e) => setDateFrom(e.target.value)}
                  className="pl-7 pr-2 py-1.5 bg-bg-tertiary border border-border-subtle rounded text-xs font-mono text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
                />
              </div>
            </div>
            <div>
              <label className="block text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-1">To</label>
              <div className="relative">
                <Calendar size={12} className="absolute left-2 top-1/2 -translate-y-1/2 text-text-tertiary" />
                <input
                  type="date"
                  value={dateTo}
                  onChange={(e) => setDateTo(e.target.value)}
                  className="pl-7 pr-2 py-1.5 bg-bg-tertiary border border-border-subtle rounded text-xs font-mono text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
                />
              </div>
            </div>
          </div>

          {/* Category */}
          <div>
            <label className="block text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-1">Category</label>
            <FilterCombobox
              value={categoryFilter}
              options={allCategories}
              placeholder="All categories"
              onChange={setCategoryFilter}
            />
          </div>

          {/* Tag */}
          <div>
            <label className="block text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-1">Tag</label>
            <FilterCombobox
              value={tagFilter}
              options={allTags}
              placeholder="All tags"
              onChange={setTagFilter}
              icon={<Tag size={10} />}
            />
          </div>

          {/* Merchant */}
          {merchantFilter && (
            <div>
              <label className="block text-[10px] font-mono text-text-tertiary uppercase tracking-wider mb-1">Merchant</label>
              <div className="flex items-center gap-1 px-2 py-1.5 bg-bg-tertiary border border-border-subtle rounded text-xs font-mono text-text-primary">
                <span className="truncate max-w-[120px]">{merchantFilter}</span>
                <button onClick={() => setMerchantFilter("")} className="text-text-tertiary hover:text-text-primary">
                  <X size={10} />
                </button>
              </div>
            </div>
          )}

          {/* Clear all */}
          {activeFilterCount > 0 && (
            <button
              onClick={clearAllFilters}
              className="px-2 py-1.5 text-[10px] font-mono text-text-tertiary hover:text-text-primary transition-colors"
            >
              Clear all
            </button>
          )}
        </div>
      )}

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
                  {headerCell("date", "Date", "px-4 py-3 font-medium")}
                  {headerCell("description", "Description", "px-4 py-3 font-medium")}
                  {headerCell("merchant", "Merchant", "px-4 py-3 font-medium")}
                  {headerCell("amount", "Amount", "px-4 py-3 font-medium text-right")}
                  {headerCell("category", "Category", "px-4 py-3 font-medium")}
                  <th className="px-4 py-3 font-medium">Account</th>
                  <th className="px-4 py-3 font-medium">Status</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border-subtle">
                {displayRows.map((row) => {
                  if (row.kind === "transfer") {
                    const t = row.data;
                    const d = new Date(t.date);
                    return (
                      <tr
                        key={`transfer-${t.id}`}
                        className="bg-accent-glow/30 hover:bg-accent-glow/50 transition-colors"
                      >
                        <td className="px-4 py-2.5 font-mono text-text-secondary text-xs whitespace-nowrap">
                          {d.toLocaleDateString("en-GB", { day: "2-digit", month: "short", year: "numeric" })}
                        </td>
                        <td className="px-4 py-2.5" colSpan={2}>
                          <div className="flex items-center gap-1.5 text-xs">
                            <span className="font-mono text-text-secondary">{t.fromAccountName}</span>
                            <ArrowRight size={12} className="text-accent shrink-0" />
                            <span className="font-mono text-text-secondary">{t.toAccountName}</span>
                          </div>
                        </td>
                        <td className="px-4 py-2.5 text-right font-mono font-medium whitespace-nowrap text-accent">
                          {formatMoney(parseFloat(t.amount), t.currency)}
                        </td>
                        <td className="px-4 py-2.5">
                          <span className="inline-block px-2 py-0.5 rounded text-[10px] font-mono bg-accent-glow text-accent">
                            Transfer
                          </span>
                        </td>
                        <td className="px-4 py-2.5" colSpan={2}>
                          <span className="text-[10px] font-mono text-text-tertiary">
                            <ArrowRightLeft size={10} className="inline mr-1" />
                            Internal
                          </span>
                        </td>
                      </tr>
                    );
                  }

                  const r = row.data;
                  const tx = r.transaction;
                  const d = new Date(tx.date);
                  const amount = parseFloat(tx.amount);
                  return (
                    <tr key={tx.id} className="hover:bg-bg-hover transition-colors">
                      <td className="px-4 py-2.5 font-mono text-text-secondary text-xs whitespace-nowrap">
                        {d.toLocaleDateString("en-GB", { day: "2-digit", month: "short", year: "numeric" })}
                        <span className="text-text-tertiary ml-1.5">
                          {d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" })}
                        </span>
                      </td>
                      <td className="px-4 py-2.5 text-text-secondary max-w-[300px] truncate" title={tx.description}>
                        {tx.description}
                      </td>
                      <td className="px-4 py-2.5 font-mono text-text-secondary text-xs">
                        {tx.merchant ? (
                          <Link
                            href={`/merchants?selected=${encodeURIComponent(tx.merchant)}`}
                            className="hover:text-accent transition-colors underline decoration-border-subtle underline-offset-2 hover:decoration-accent"
                          >
                            {tx.merchant}
                          </Link>
                        ) : (
                          <span className="text-text-tertiary">--</span>
                        )}
                      </td>
                      <td className={cn("px-4 py-2.5 text-right font-mono font-medium whitespace-nowrap", pnlColor(amount))}>
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
                        <span className={cn(
                          "inline-block px-2 py-0.5 rounded text-[10px] font-mono font-medium uppercase",
                          STATUS_COLORS[tx.status] || "bg-bg-tertiary text-text-tertiary"
                        )}>
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

/* ---- Filter combobox (search + select from options) ---- */
function FilterCombobox({
  value,
  options,
  placeholder,
  onChange,
  icon,
}: {
  value: string;
  options: string[];
  placeholder: string;
  onChange: (value: string) => void;
  icon?: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  const filtered = query
    ? options.filter((o) => o.toLowerCase().includes(query.toLowerCase()))
    : options;

  function select(val: string) {
    onChange(val);
    setQuery("");
    setOpen(false);
  }

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => { setOpen(!open); setQuery(""); }}
        className="flex items-center gap-1.5 px-2 py-1.5 bg-bg-tertiary border border-border-subtle rounded text-xs font-mono text-text-primary min-w-[130px] max-w-[200px]"
      >
        {icon}
        <span className={cn("truncate flex-1 text-left", !value && "text-text-tertiary")}>
          {value || placeholder}
        </span>
        {value ? (
          <button
            onClick={(e) => { e.stopPropagation(); onChange(""); setOpen(false); }}
            className="text-text-tertiary hover:text-text-primary"
          >
            <X size={10} />
          </button>
        ) : (
          <ChevronsUpDown size={10} className="text-text-tertiary shrink-0" />
        )}
      </button>

      {open && (
        <div className="absolute z-50 mt-1 w-56 bg-bg-secondary border border-border-subtle rounded-lg shadow-lg overflow-hidden">
          <div className="p-1.5">
            <input
              autoFocus
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && filtered.length === 1) select(filtered[0]);
                if (e.key === "Escape") setOpen(false);
              }}
              placeholder="Search..."
              className="w-full px-2 py-1.5 bg-bg-tertiary border border-border-subtle rounded text-xs font-mono text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </div>
          <div className="max-h-48 overflow-y-auto">
            <button
              onClick={() => select("")}
              className={cn(
                "w-full text-left px-3 py-1.5 text-xs font-mono hover:bg-bg-hover transition-colors flex items-center gap-2",
                !value && "text-accent"
              )}
            >
              {!value && <Check size={10} />}
              <span className="text-text-tertiary italic">{placeholder}</span>
            </button>
            {filtered.map((opt) => (
              <button
                key={opt}
                onClick={() => select(opt)}
                className={cn(
                  "w-full text-left px-3 py-1.5 text-xs font-mono hover:bg-bg-hover transition-colors flex items-center gap-2",
                  value === opt && "text-accent"
                )}
              >
                {value === opt && <Check size={10} />}
                <span className="truncate">{opt}</span>
              </button>
            ))}
            {filtered.length === 0 && (
              <p className="px-3 py-2 text-xs text-text-tertiary text-center">No matches</p>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
