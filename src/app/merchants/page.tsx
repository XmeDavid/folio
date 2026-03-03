"use client";

import { Suspense, useEffect, useState, useCallback, useRef } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { Card } from "@/components/ui/card";
import { LoadingSpinner } from "@/components/ui/loading";
import { cn, formatMoney, pnlColor } from "@/lib/utils";
import {
  Search,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  ChevronDown,
  ChevronsUpDown,
  X,
  Plus,
  Check,
} from "lucide-react";

interface MerchantRow {
  merchant: string;
  tx_count: number;
  total_spent: string;
  override_category: string | null;
}

interface TransactionRow {
  transaction: {
    id: string;
    date: string;
    description: string;
    amount: string;
    currency: string;
    category: string | null;
    categoryManual: boolean;
    merchant: string | null;
    tags: string[];
    status: string;
  };
  accountName: string;
  accountType: string;
}

type SortKey = "name" | "count" | "total";
type SortDir = "asc" | "desc";

export default function MerchantsPage() {
  return (
    <Suspense fallback={<div className="py-20"><LoadingSpinner /></div>}>
      <MerchantsContent />
    </Suspense>
  );
}

function MerchantsContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const selectedParam = searchParams.get("selected");

  const [merchants, setMerchants] = useState<MerchantRow[]>([]);
  const [merchantTotal, setMerchantTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [sortBy, setSortBy] = useState<SortKey>("count");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  const [selected, setSelected] = useState<string | null>(selectedParam);
  const [allCategories, setAllCategories] = useState<string[]>([]);
  const [allTags, setAllTags] = useState<string[]>([]);
  const [transactions, setTransactions] = useState<TransactionRow[]>([]);
  const [txTotal, setTxTotal] = useState(0);
  const [txPage, setTxPage] = useState(0);
  const [txLoading, setTxLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [selectedCategory, setSelectedCategory] = useState<string | null>(null);

  const txLimit = 30;

  const fetchMerchants = useCallback(async () => {
    setLoading(true);
    const params = new URLSearchParams({ sortBy, sortDir, limit: "200" });
    if (search) params.set("search", search);
    const res = await fetch(`/api/merchants?${params}`);
    const json = await res.json();
    setMerchants(json.data);
    setMerchantTotal(json.total);
    setLoading(false);
  }, [search, sortBy, sortDir]);

  // Fetch categories + tags once
  function refreshCategories() {
    fetch("/api/categories")
      .then((r) => r.json())
      .then((json) => setAllCategories(json.data));
  }
  function refreshTags() {
    fetch("/api/tags")
      .then((r) => r.json())
      .then((json) => setAllTags(json.data));
  }
  useEffect(() => {
    refreshCategories();
    refreshTags();
  }, []);

  useEffect(() => {
    fetchMerchants();
  }, [fetchMerchants]);

  const fetchTransactions = useCallback(async () => {
    if (!selected) return;
    setTxLoading(true);
    const params = new URLSearchParams({
      merchant: selected,
      limit: txLimit.toString(),
      offset: (txPage * txLimit).toString(),
      sortBy: "date",
      sortDir: "desc",
      excludeTransfers: "false",
    });
    const res = await fetch(`/api/banking/transactions?${params}`);
    const json = await res.json();
    setTransactions(json.data);
    setTxTotal(json.total);
    setTxLoading(false);
  }, [selected, txPage]);

  useEffect(() => {
    if (selected) {
      fetchTransactions();
      const m = merchants.find((m) => m.merchant === selected);
      setSelectedCategory(m?.override_category ?? null);
    } else {
      setTransactions([]);
      setTxTotal(0);
    }
  }, [selected, fetchTransactions]);

  function selectMerchant(name: string) {
    setSelected(name);
    setTxPage(0);
    router.replace(`/merchants?selected=${encodeURIComponent(name)}`, { scroll: false });
  }

  async function saveMerchantCategory(category: string | null) {
    if (!selected) return;
    setSaving(true);
    await fetch(`/api/merchants/${encodeURIComponent(selected)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ category }),
    });
    setSelectedCategory(category);
    setSaving(false);
    fetchMerchants();
    fetchTransactions();
    refreshCategories();
  }

  async function updateTransactionCategory(txId: string, category: string | null) {
    await fetch(`/api/banking/transactions/${txId}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ category }),
    });
    fetchTransactions();
    refreshCategories();
  }

  async function updateTransactionTags(txId: string, tags: string[]) {
    await fetch(`/api/banking/transactions/${txId}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ tags }),
    });
    fetchTransactions();
    refreshTags();
  }

  function toggleSort(key: SortKey) {
    if (sortBy === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortBy(key);
      setSortDir(key === "name" ? "asc" : "desc");
    }
  }

  function sortIcon(key: SortKey) {
    if (sortBy !== key)
      return <ChevronsUpDown size={12} className="text-text-tertiary/70" />;
    return sortDir === "asc" ? (
      <ChevronUp size={12} className="text-accent" />
    ) : (
      <ChevronDown size={12} className="text-accent" />
    );
  }

  const txTotalPages = Math.ceil(txTotal / txLimit);
  const selectedMerchant = merchants.find((m) => m.merchant === selected);

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold tracking-tight">Merchants</h2>
        <p className="text-sm text-text-tertiary mt-0.5">
          Manage merchant categories and transaction overrides
        </p>
      </div>

      <div className="flex flex-col md:flex-row gap-6">
        {/* Left panel - Merchant list */}
        <div className="w-full md:w-80 shrink-0 space-y-3">
          <div className="relative">
            <Search
              size={14}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary"
            />
            <input
              type="text"
              placeholder="Search merchants..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-3 py-2 bg-bg-tertiary border border-border-subtle rounded-lg text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent font-mono"
            />
          </div>

          <Card>
            {loading ? (
              <div className="py-12">
                <LoadingSpinner />
              </div>
            ) : (
              <div className="max-h-[calc(100vh-260px)] overflow-y-auto">
                <div className="flex items-center gap-2 px-3 py-2 border-b border-border-subtle text-[10px] font-mono text-text-tertiary uppercase tracking-wider">
                  <button
                    onClick={() => toggleSort("name")}
                    className="flex items-center gap-0.5 hover:text-text-secondary"
                  >
                    Name {sortIcon("name")}
                  </button>
                  <button
                    onClick={() => toggleSort("count")}
                    className="flex items-center gap-0.5 hover:text-text-secondary ml-auto"
                  >
                    Count {sortIcon("count")}
                  </button>
                  <button
                    onClick={() => toggleSort("total")}
                    className="flex items-center gap-0.5 hover:text-text-secondary"
                  >
                    Total {sortIcon("total")}
                  </button>
                </div>

                {merchants.length === 0 ? (
                  <p className="px-4 py-8 text-center text-sm text-text-tertiary">
                    No merchants found
                  </p>
                ) : (
                  merchants.map((m) => (
                    <button
                      key={m.merchant}
                      onClick={() => selectMerchant(m.merchant)}
                      className={cn(
                        "w-full text-left px-3 py-2.5 border-b border-border-subtle last:border-0 hover:bg-bg-hover transition-colors",
                        selected === m.merchant && "bg-bg-hover"
                      )}
                    >
                      <div className="flex items-center gap-2">
                        <span className="text-sm text-text-primary font-medium truncate flex-1">
                          {m.merchant}
                        </span>
                        <span className="text-[10px] font-mono text-text-tertiary shrink-0">
                          {m.tx_count}
                        </span>
                      </div>
                      <div className="flex items-center gap-2 mt-0.5">
                        {m.override_category ? (
                          <span className="inline-block px-1.5 py-0.5 rounded text-[10px] font-mono bg-accent-glow text-accent truncate max-w-[140px]">
                            {m.override_category}
                          </span>
                        ) : (
                          <span className="text-[10px] font-mono text-text-tertiary">
                            No category
                          </span>
                        )}
                        <span className="text-[10px] font-mono text-text-tertiary ml-auto">
                          {formatMoney(parseFloat(m.total_spent), "CHF")}
                        </span>
                      </div>
                    </button>
                  ))
                )}
              </div>
            )}
          </Card>
          <p className="text-[10px] font-mono text-text-tertiary text-center">
            {merchantTotal} merchants
          </p>
        </div>

        {/* Right panel - Merchant detail */}
        <div className="flex-1 min-w-0">
          {!selected ? (
            <Card>
              <div className="py-20 text-center text-sm text-text-tertiary">
                Select a merchant to view details
              </div>
            </Card>
          ) : (
            <div className="space-y-4">
              {/* Header + category */}
              <Card>
                <div className="p-4 space-y-3">
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <h3 className="text-lg font-semibold text-text-primary">
                        {selected}
                      </h3>
                      {selectedMerchant && (
                        <p className="text-xs font-mono text-text-tertiary mt-0.5">
                          {selectedMerchant.tx_count} transactions &middot;{" "}
                          {formatMoney(
                            parseFloat(selectedMerchant.total_spent),
                            "CHF"
                          )}{" "}
                          spent
                        </p>
                      )}
                    </div>
                    <button
                      onClick={() => {
                        setSelected(null);
                        router.replace("/merchants", { scroll: false });
                      }}
                      className="p-1 text-text-tertiary hover:text-text-primary"
                    >
                      <X size={16} />
                    </button>
                  </div>

                  <div className="flex items-center gap-2">
                    <label className="text-xs font-mono text-text-tertiary shrink-0">
                      Default category:
                    </label>
                    <CategoryCombobox
                      value={selectedCategory}
                      options={allCategories}
                      onChange={saveMerchantCategory}
                      disabled={saving}
                    />
                    {saving && (
                      <span className="text-[10px] font-mono text-text-tertiary">
                        Saving...
                      </span>
                    )}
                  </div>
                </div>
              </Card>

              {/* Transactions */}
              <Card>
                <div className="overflow-x-auto">
                  {txLoading ? (
                    <div className="py-12">
                      <LoadingSpinner />
                    </div>
                  ) : (
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="text-left text-[11px] text-text-tertiary uppercase tracking-wider font-mono border-b border-border-subtle">
                          <th className="px-4 py-3 font-medium">Date</th>
                          <th className="px-4 py-3 font-medium">Description</th>
                          <th className="px-4 py-3 font-medium text-right">Amount</th>
                          <th className="px-4 py-3 font-medium">Category</th>
                          <th className="px-4 py-3 font-medium">Tags</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-border-subtle">
                        {transactions.map((r) => {
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
                              </td>
                              <td
                                className="px-4 py-2.5 text-text-secondary max-w-[260px] truncate"
                                title={tx.description}
                              >
                                {tx.description}
                              </td>
                              <td
                                className={cn(
                                  "px-4 py-2.5 text-right font-mono font-medium whitespace-nowrap",
                                  pnlColor(amount)
                                )}
                              >
                                {formatMoney(amount, tx.currency)}
                              </td>
                              <td className="px-4 py-2.5">
                                <CategoryCell
                                  txId={tx.id}
                                  category={tx.category}
                                  isManual={tx.categoryManual}
                                  options={allCategories}
                                  onUpdate={updateTransactionCategory}
                                />
                              </td>
                              <td className="px-4 py-2.5">
                                <TagsCell
                                  txId={tx.id}
                                  tags={tx.tags ?? []}
                                  allTags={allTags}
                                  onUpdate={updateTransactionTags}
                                />
                              </td>
                            </tr>
                          );
                        })}
                        {transactions.length === 0 && (
                          <tr>
                            <td
                              colSpan={5}
                              className="px-4 py-8 text-center text-sm text-text-tertiary"
                            >
                              No transactions
                            </td>
                          </tr>
                        )}
                      </tbody>
                    </table>
                  )}
                </div>

                {txTotalPages > 1 && (
                  <div className="flex items-center justify-between px-4 py-3 border-t border-border-subtle">
                    <button
                      onClick={() => setTxPage((p) => Math.max(0, p - 1))}
                      disabled={txPage === 0}
                      className="flex items-center gap-1 px-3 py-1.5 text-xs font-mono text-text-secondary hover:text-text-primary disabled:opacity-30 disabled:cursor-not-allowed"
                    >
                      <ChevronLeft size={14} /> Prev
                    </button>
                    <span className="text-xs font-mono text-text-tertiary">
                      Page {txPage + 1} of {txTotalPages}
                    </span>
                    <button
                      onClick={() =>
                        setTxPage((p) => Math.min(txTotalPages - 1, p + 1))
                      }
                      disabled={txPage >= txTotalPages - 1}
                      className="flex items-center gap-1 px-3 py-1.5 text-xs font-mono text-text-secondary hover:text-text-primary disabled:opacity-30 disabled:cursor-not-allowed"
                    >
                      Next <ChevronRight size={14} />
                    </button>
                  </div>
                )}
              </Card>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

/* ---- Category combobox (type to search/create, pick from existing) ---- */
function CategoryCombobox({
  value,
  options,
  onChange,
  disabled,
  small,
}: {
  value: string | null;
  options: string[];
  onChange: (value: string | null) => void;
  disabled?: boolean;
  small?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  const filtered = query
    ? options.filter((o) => o.toLowerCase().includes(query.toLowerCase()))
    : options;

  const showCreate =
    query.trim() &&
    !options.some((o) => o.toLowerCase() === query.trim().toLowerCase());

  function select(val: string | null) {
    onChange(val);
    setQuery("");
    setOpen(false);
  }

  const sz = small ? "text-[10px] px-1.5 py-0.5" : "text-sm px-2 py-1";

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => {
          if (!disabled) {
            setOpen(!open);
            setQuery("");
          }
        }}
        disabled={disabled}
        className={cn(
          "bg-bg-tertiary border border-border-subtle rounded font-mono text-left flex items-center gap-1 min-w-[120px] max-w-[240px]",
          sz,
          disabled && "opacity-50 cursor-not-allowed"
        )}
      >
        <span className={cn("truncate flex-1", !value && "text-text-tertiary")}>
          {value || "None"}
        </span>
        <ChevronsUpDown size={small ? 8 : 10} className="text-text-tertiary shrink-0" />
      </button>

      {open && (
        <div className="absolute z-50 mt-1 w-64 bg-bg-secondary border border-border-subtle rounded-lg shadow-lg overflow-hidden">
          <div className="p-1.5">
            <input
              autoFocus
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  if (showCreate && query.trim()) {
                    select(query.trim());
                  } else if (filtered.length === 1) {
                    select(filtered[0]);
                  }
                }
                if (e.key === "Escape") setOpen(false);
              }}
              placeholder="Search or type new..."
              className="w-full px-2 py-1.5 bg-bg-tertiary border border-border-subtle rounded text-xs font-mono text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </div>
          <div className="max-h-48 overflow-y-auto">
            {/* None option */}
            <button
              onClick={() => select(null)}
              className={cn(
                "w-full text-left px-3 py-1.5 text-xs font-mono hover:bg-bg-hover transition-colors flex items-center gap-2",
                !value && "text-accent"
              )}
            >
              {!value && <Check size={10} />}
              <span className="text-text-tertiary italic">None</span>
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
            {showCreate && (
              <button
                onClick={() => select(query.trim())}
                className="w-full text-left px-3 py-1.5 text-xs font-mono hover:bg-bg-hover transition-colors flex items-center gap-2 text-accent border-t border-border-subtle"
              >
                <Plus size={10} />
                <span>Create &quot;{query.trim()}&quot;</span>
              </button>
            )}
            {filtered.length === 0 && !showCreate && (
              <p className="px-3 py-2 text-xs text-text-tertiary text-center">
                No matches
              </p>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

/* ---- Inline category editor for transaction rows ---- */
function CategoryCell({
  txId,
  category,
  isManual,
  options,
  onUpdate,
}: {
  txId: string;
  category: string | null;
  isManual: boolean;
  options: string[];
  onUpdate: (txId: string, category: string | null) => void;
}) {
  const [editing, setEditing] = useState(false);

  if (editing) {
    return (
      <CategoryCombobox
        value={category}
        options={options}
        onChange={(val) => {
          onUpdate(txId, val);
          setEditing(false);
        }}
        small
      />
    );
  }

  return (
    <button
      onClick={() => setEditing(true)}
      className="group flex items-center gap-1"
      title={isManual ? "Manually set (click to change)" : "Click to override"}
    >
      {category ? (
        <span
          className={cn(
            "inline-block px-2 py-0.5 rounded text-[10px] font-mono truncate max-w-[140px]",
            isManual
              ? "bg-yellow-dim text-yellow"
              : "bg-bg-tertiary text-text-secondary"
          )}
        >
          {category}
        </span>
      ) : (
        <span className="text-text-tertiary text-xs group-hover:text-text-secondary">
          --
        </span>
      )}
    </button>
  );
}

/* ---- Inline tags editor with autocomplete ---- */
function TagsCell({
  txId,
  tags,
  allTags,
  onUpdate,
}: {
  txId: string;
  tags: string[];
  allTags: string[];
  onUpdate: (txId: string, tags: string[]) => void;
}) {
  const [adding, setAdding] = useState(false);
  const [query, setQuery] = useState("");
  const [showSuggestions, setShowSuggestions] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setShowSuggestions(false);
      }
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  const suggestions = query
    ? allTags.filter(
        (t) =>
          t.toLowerCase().includes(query.toLowerCase()) && !tags.includes(t)
      )
    : allTags.filter((t) => !tags.includes(t));

  const showCreate =
    query.trim() &&
    !allTags.some((t) => t.toLowerCase() === query.trim().toLowerCase()) &&
    !tags.includes(query.trim().toLowerCase());

  function addTag(tag: string) {
    const normalized = tag.trim().toLowerCase();
    if (normalized && !tags.includes(normalized)) {
      onUpdate(txId, [...tags, normalized]);
    }
    setQuery("");
    setAdding(false);
    setShowSuggestions(false);
  }

  function removeTag(tag: string) {
    onUpdate(
      txId,
      tags.filter((t) => t !== tag)
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-1" ref={ref}>
      {tags.map((tag) => (
        <span
          key={tag}
          className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-[10px] font-mono bg-bg-tertiary text-text-secondary"
        >
          {tag}
          <button
            onClick={() => removeTag(tag)}
            className="hover:text-red ml-0.5"
          >
            <X size={8} />
          </button>
        </span>
      ))}
      {adding ? (
        <div className="relative">
          <input
            autoFocus
            type="text"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setShowSuggestions(true);
            }}
            onFocus={() => setShowSuggestions(true)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && query.trim()) {
                addTag(query);
              }
              if (e.key === "Escape") {
                setAdding(false);
                setQuery("");
                setShowSuggestions(false);
              }
            }}
            placeholder="tag..."
            className="w-20 px-1 py-0.5 bg-bg-tertiary border border-border-subtle rounded text-[10px] font-mono text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
          />
          {showSuggestions && (suggestions.length > 0 || showCreate) && (
            <div className="absolute z-50 mt-1 w-36 bg-bg-secondary border border-border-subtle rounded shadow-lg overflow-hidden">
              <div className="max-h-32 overflow-y-auto">
                {suggestions.slice(0, 8).map((t) => (
                  <button
                    key={t}
                    onClick={() => addTag(t)}
                    className="w-full text-left px-2 py-1 text-[10px] font-mono hover:bg-bg-hover transition-colors text-text-secondary"
                  >
                    {t}
                  </button>
                ))}
                {showCreate && (
                  <button
                    onClick={() => addTag(query)}
                    className="w-full text-left px-2 py-1 text-[10px] font-mono hover:bg-bg-hover transition-colors text-accent border-t border-border-subtle flex items-center gap-1"
                  >
                    <Plus size={8} />
                    {query.trim().toLowerCase()}
                  </button>
                )}
              </div>
            </div>
          )}
        </div>
      ) : (
        <button
          onClick={() => setAdding(true)}
          className="p-0.5 text-text-tertiary hover:text-text-secondary"
          title="Add tag"
        >
          <Plus size={10} />
        </button>
      )}
    </div>
  );
}
