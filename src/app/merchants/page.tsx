"use client";

import { Suspense, useEffect, useState, useCallback } from "react";
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
} from "lucide-react";

interface MerchantRow {
  merchant: string;
  tx_count: number;
  total_spent: string;
  override_category: string | null;
}

interface CategoryOption {
  id: number;
  name: string;
  parentGroup: string | null;
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
  const [categories, setCategories] = useState<CategoryOption[]>([]);
  const [transactions, setTransactions] = useState<TransactionRow[]>([]);
  const [txTotal, setTxTotal] = useState(0);
  const [txPage, setTxPage] = useState(0);
  const [txLoading, setTxLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [selectedCategory, setSelectedCategory] = useState<string | null>(null);

  const txLimit = 30;

  // Fetch merchants
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

  // Fetch categories once
  useEffect(() => {
    fetch("/api/categories")
      .then((r) => r.json())
      .then((json) => setCategories(json.data));
  }, []);

  useEffect(() => {
    fetchMerchants();
  }, [fetchMerchants]);

  // Fetch detail when selected changes
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
      // Set the category from override
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
  }

  async function updateTransactionCategory(txId: string, category: string | null) {
    await fetch(`/api/banking/transactions/${txId}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ category }),
    });
    fetchTransactions();
  }

  async function updateTransactionTags(txId: string, tags: string[]) {
    await fetch(`/api/banking/transactions/${txId}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ tags }),
    });
    fetchTransactions();
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
                {/* Sort controls */}
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
                    <label className="text-xs font-mono text-text-tertiary">
                      Default category:
                    </label>
                    <select
                      value={selectedCategory ?? ""}
                      onChange={(e) => {
                        const val = e.target.value || null;
                        saveMerchantCategory(val);
                      }}
                      disabled={saving}
                      className="bg-bg-tertiary border border-border-subtle rounded px-2 py-1 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent font-mono appearance-none cursor-pointer"
                    >
                      <option value="">None</option>
                      {categories.map((c) => (
                        <option key={c.id} value={c.name}>
                          {c.name}
                        </option>
                      ))}
                    </select>
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
                          <th className="px-4 py-3 font-medium text-right">
                            Amount
                          </th>
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
                                  categories={categories}
                                  onUpdate={updateTransactionCategory}
                                />
                              </td>
                              <td className="px-4 py-2.5">
                                <TagsCell
                                  txId={tx.id}
                                  tags={tx.tags ?? []}
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

/* ---- Inline category editor ---- */
function CategoryCell({
  txId,
  category,
  isManual,
  categories,
  onUpdate,
}: {
  txId: string;
  category: string | null;
  isManual: boolean;
  categories: CategoryOption[];
  onUpdate: (txId: string, category: string | null) => void;
}) {
  const [editing, setEditing] = useState(false);

  if (editing) {
    return (
      <select
        autoFocus
        value={category ?? ""}
        onChange={(e) => {
          onUpdate(txId, e.target.value || null);
          setEditing(false);
        }}
        onBlur={() => setEditing(false)}
        className="bg-bg-tertiary border border-border-subtle rounded px-1.5 py-0.5 text-[10px] font-mono text-text-primary focus:outline-none focus:ring-1 focus:ring-accent appearance-none cursor-pointer"
      >
        <option value="">None</option>
        {categories.map((c) => (
          <option key={c.id} value={c.name}>
            {c.name}
          </option>
        ))}
      </select>
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

/* ---- Inline tags editor ---- */
function TagsCell({
  txId,
  tags,
  onUpdate,
}: {
  txId: string;
  tags: string[];
  onUpdate: (txId: string, tags: string[]) => void;
}) {
  const [adding, setAdding] = useState(false);
  const [newTag, setNewTag] = useState("");

  function addTag() {
    const tag = newTag.trim().toLowerCase();
    if (tag && !tags.includes(tag)) {
      onUpdate(txId, [...tags, tag]);
    }
    setNewTag("");
    setAdding(false);
  }

  function removeTag(tag: string) {
    onUpdate(
      txId,
      tags.filter((t) => t !== tag)
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-1">
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
        <input
          autoFocus
          type="text"
          value={newTag}
          onChange={(e) => setNewTag(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") addTag();
            if (e.key === "Escape") {
              setAdding(false);
              setNewTag("");
            }
          }}
          onBlur={addTag}
          placeholder="tag..."
          className="w-16 px-1 py-0.5 bg-bg-tertiary border border-border-subtle rounded text-[10px] font-mono text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
        />
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
