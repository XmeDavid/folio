"use client";

import { useEffect, useState, useCallback } from "react";
import { Card } from "@/components/ui/card";
import { LoadingSpinner } from "@/components/ui/loading";
import { cn, formatMoney, pnlColor } from "@/lib/utils";
import Link from "next/link";
import {
  Plus,
  Pencil,
  Trash2,
  Check,
  X,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  ChevronDown,
  ChevronsUpDown,
  Search,
  Building2,
  ArrowLeftRight,
  Wallet,
} from "lucide-react";

/* ─── Types ─── */

interface AccountRow {
  id: string;
  name: string;
  broker: string;
  type: string;
  baseCurrency: string;
  createdAt: string;
  investmentCount: number;
  bankingCount: number;
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

interface BankingTxRow {
  transaction: {
    id: string;
    date: string;
    description: string;
    amount: string;
    currency: string;
    balance: string | null;
    status: string;
    category: string | null;
    merchant: string | null;
  };
  accountName: string;
  accountType: string;
}

/* ─── Constants ─── */

const TYPE_COLORS: Record<string, string> = {
  investment: "bg-accent-glow text-accent",
  checking: "bg-green-dim text-green",
  savings: "bg-yellow-dim text-yellow",
};

const TX_TYPE_COLORS: Record<string, string> = {
  "BUY - MARKET": "bg-green-dim text-green",
  "BUY - LIMIT": "bg-green-dim text-green",
  BUY: "bg-green-dim text-green",
  "SELL - MARKET": "bg-red-dim text-red",
  "SELL - LIMIT": "bg-red-dim text-red",
  DIVIDEND: "bg-yellow-dim text-yellow",
  "CUSTODY FEE": "bg-red-dim text-red",
  "CASH TOP-UP": "bg-accent-glow text-accent",
};

const STATUS_COLORS: Record<string, string> = {
  completed: "bg-green-dim text-green",
  reversed: "bg-red-dim text-red",
  pending: "bg-yellow-dim text-yellow",
};

/* ─── Create Account Form ─── */

function CreateAccountForm({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [broker, setBroker] = useState("");
  const [type, setType] = useState("investment");
  const [currency, setCurrency] = useState("USD");
  const [saving, setSaving] = useState(false);

  async function handleCreate() {
    if (!name.trim() || !broker.trim()) return;
    setSaving(true);
    try {
      const res = await fetch("/api/accounts", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name.trim(), broker: broker.trim(), type, baseCurrency: currency }),
      });
      if (!res.ok) throw new Error();
      setName("");
      setBroker("");
      setType("investment");
      setCurrency("USD");
      setOpen(false);
      onCreated();
    } finally {
      setSaving(false);
    }
  }

  const inputClass =
    "w-full bg-bg-tertiary border border-border-subtle rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent font-mono";
  const labelClass =
    "block text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1.5";

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="flex items-center gap-1.5 px-3 py-2 text-xs font-mono text-text-secondary hover:text-accent transition-colors"
      >
        <Plus size={14} /> New Account
      </button>
    );
  }

  return (
    <div className="p-4 border border-border-subtle rounded-lg bg-bg-secondary space-y-3">
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className={labelClass}>Name</label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="My Account"
            className={inputClass}
            autoFocus
          />
        </div>
        <div>
          <label className={labelClass}>Broker</label>
          <input
            type="text"
            value={broker}
            onChange={(e) => setBroker(e.target.value)}
            placeholder="Revolut"
            className={inputClass}
          />
        </div>
        <div>
          <label className={labelClass}>Type</label>
          <select value={type} onChange={(e) => setType(e.target.value)} className={inputClass}>
            <option value="investment">Investment</option>
            <option value="checking">Checking</option>
            <option value="savings">Savings</option>
          </select>
        </div>
        <div>
          <label className={labelClass}>Currency</label>
          <select value={currency} onChange={(e) => setCurrency(e.target.value)} className={inputClass}>
            <option value="USD">USD</option>
            <option value="EUR">EUR</option>
            <option value="CHF">CHF</option>
            <option value="GBP">GBP</option>
          </select>
        </div>
      </div>
      <div className="flex items-center gap-2">
        <button
          onClick={handleCreate}
          disabled={!name.trim() || !broker.trim() || saving}
          className="px-4 py-1.5 bg-accent text-bg-primary rounded-lg text-xs font-medium hover:opacity-90 disabled:opacity-40 transition-opacity"
        >
          {saving ? "Creating..." : "Create"}
        </button>
        <button
          onClick={() => setOpen(false)}
          className="px-3 py-1.5 text-xs text-text-tertiary hover:text-text-secondary transition-colors"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

/* ─── Account List Item ─── */

function AccountItem({
  account,
  selected,
  onSelect,
  onRenamed,
  onDeleted,
}: {
  account: AccountRow;
  selected: boolean;
  onSelect: () => void;
  onRenamed: () => void;
  onDeleted: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [editName, setEditName] = useState(account.name);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState("");

  const totalTx = account.investmentCount + account.bankingCount;

  async function handleRename() {
    if (!editName.trim() || editName.trim() === account.name) {
      setEditing(false);
      setEditName(account.name);
      return;
    }
    await fetch(`/api/accounts/${account.id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: editName.trim() }),
    });
    setEditing(false);
    onRenamed();
  }

  async function handleDelete() {
    setDeleting(true);
    setDeleteError("");
    const res = await fetch(`/api/accounts/${account.id}`, { method: "DELETE" });
    if (!res.ok) {
      const json = await res.json();
      setDeleteError(json.error || "Failed to delete");
      setDeleting(false);
      return;
    }
    setDeleting(false);
    setConfirmDelete(false);
    onDeleted();
  }

  return (
    <div
      className={cn(
        "px-4 py-3 border-b border-border-subtle transition-colors cursor-pointer",
        selected ? "bg-bg-hover" : "hover:bg-bg-hover/50"
      )}
      onClick={() => !editing && !confirmDelete && onSelect()}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex-1 min-w-0">
          {editing ? (
            <div className="flex items-center gap-1.5" onClick={(e) => e.stopPropagation()}>
              <input
                type="text"
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") handleRename();
                  if (e.key === "Escape") {
                    setEditing(false);
                    setEditName(account.name);
                  }
                }}
                className="bg-bg-tertiary border border-accent rounded px-2 py-0.5 text-sm text-text-primary font-mono focus:outline-none w-full"
                autoFocus
              />
              <button onClick={handleRename} className="text-green hover:opacity-80 shrink-0">
                <Check size={14} />
              </button>
              <button
                onClick={() => {
                  setEditing(false);
                  setEditName(account.name);
                }}
                className="text-text-tertiary hover:text-text-secondary shrink-0"
              >
                <X size={14} />
              </button>
            </div>
          ) : (
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium text-text-primary truncate">
                {account.name}
              </span>
              <span
                className={cn(
                  "inline-block px-1.5 py-0.5 rounded text-[9px] font-mono font-medium uppercase shrink-0",
                  TYPE_COLORS[account.type] || "bg-bg-tertiary text-text-tertiary"
                )}
              >
                {account.type}
              </span>
            </div>
          )}
          <div className="flex items-center gap-2 mt-0.5">
            <span className="text-[11px] font-mono text-text-tertiary">{account.broker}</span>
            <span className="text-[11px] text-text-tertiary">·</span>
            <span className="text-[11px] font-mono text-text-tertiary">{account.baseCurrency}</span>
            <span className="text-[11px] text-text-tertiary">·</span>
            <span className="text-[11px] font-mono text-text-tertiary">
              {totalTx} tx
            </span>
          </div>
        </div>

        {!editing && !confirmDelete && (
          <div className="flex items-center gap-1 shrink-0" onClick={(e) => e.stopPropagation()}>
            <button
              onClick={() => {
                setEditName(account.name);
                setEditing(true);
              }}
              className="p-1 text-text-tertiary hover:text-text-secondary transition-colors rounded"
              title="Rename"
            >
              <Pencil size={12} />
            </button>
            <button
              onClick={() => setConfirmDelete(true)}
              className="p-1 text-text-tertiary hover:text-red transition-colors rounded"
              title="Delete"
            >
              <Trash2 size={12} />
            </button>
          </div>
        )}
      </div>

      {confirmDelete && (
        <div
          className="mt-2 p-2 bg-bg-tertiary rounded border border-border-subtle"
          onClick={(e) => e.stopPropagation()}
        >
          {totalTx > 0 ? (
            <p className="text-[11px] text-yellow font-mono mb-1.5">
              This account has {totalTx} transactions. Delete them first.
            </p>
          ) : (
            <p className="text-[11px] text-text-secondary font-mono mb-1.5">
              Delete this account?
            </p>
          )}
          {deleteError && (
            <p className="text-[11px] text-red font-mono mb-1.5">{deleteError}</p>
          )}
          <div className="flex items-center gap-2">
            <button
              onClick={handleDelete}
              disabled={totalTx > 0 || deleting}
              className="px-3 py-1 bg-red text-bg-primary rounded text-[11px] font-medium hover:opacity-90 disabled:opacity-30 transition-opacity"
            >
              {deleting ? "Deleting..." : "Delete"}
            </button>
            <button
              onClick={() => {
                setConfirmDelete(false);
                setDeleteError("");
              }}
              className="px-3 py-1 text-[11px] text-text-tertiary hover:text-text-secondary transition-colors"
            >
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

/* ─── Trades Tab ─── */

type TradeSortKey = "date" | "ticker" | "type" | "quantity" | "unitPrice" | "totalAmount";

function TradesTab({ accountId }: { accountId: string }) {
  const [rows, setRows] = useState<TransactionRow[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(0);
  const [search, setSearch] = useState("");
  const [sortBy, setSortBy] = useState<TradeSortKey>("date");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc");
  const limit = 50;

  function toggleSort(key: TradeSortKey) {
    if (sortBy === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
      return;
    }
    setSortBy(key);
    setSortDir(key === "date" ? "desc" : "asc");
  }

  function header(key: TradeSortKey, label: string, className: string) {
    const active = sortBy === key;
    return (
      <th className={className}>
        <button
          onClick={() => toggleSort(key)}
          className="inline-flex items-center gap-1 hover:text-text-secondary transition-colors"
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
      accountId,
      limit: limit.toString(),
      offset: (page * limit).toString(),
      sortBy,
      sortDir,
    });
    if (search) params.set("ticker", search.toUpperCase());

    const res = await fetch(`/api/transactions?${params}`);
    const json = await res.json();
    setRows(json.data);
    setTotal(json.total);
    setLoading(false);
  }, [accountId, page, search, sortBy, sortDir]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  useEffect(() => {
    setPage(0);
  }, [search, sortBy, sortDir, accountId]);

  const totalPages = Math.ceil(total / limit);

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3 px-1">
        <div className="relative flex-1 max-w-xs">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary" />
          <input
            type="text"
            placeholder="Search ticker..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full pl-9 pr-3 py-1.5 bg-bg-tertiary border border-border-subtle rounded-lg text-xs text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent font-mono"
          />
        </div>
        <span className="text-[11px] font-mono text-text-tertiary ml-auto">{total} trades</span>
      </div>

      {loading ? (
        <div className="py-12"><LoadingSpinner /></div>
      ) : rows.length === 0 ? (
        <div className="py-12 text-center text-sm text-text-tertiary">No trades found</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-[11px] text-text-tertiary uppercase tracking-wider font-mono border-b border-border-subtle">
                {header("date", "Date", "px-3 py-2 font-medium")}
                {header("ticker", "Ticker", "px-3 py-2 font-medium")}
                {header("type", "Type", "px-3 py-2 font-medium")}
                {header("quantity", "Qty", "px-3 py-2 font-medium text-right")}
                {header("unitPrice", "Price", "px-3 py-2 font-medium text-right")}
                {header("totalAmount", "Total", "px-3 py-2 font-medium text-right")}
              </tr>
            </thead>
            <tbody className="divide-y divide-border-subtle">
              {rows.map((r) => {
                const tx = r.transaction;
                const d = new Date(tx.date);
                return (
                  <tr key={tx.id} className="hover:bg-bg-hover transition-colors">
                    <td className="px-3 py-2 font-mono text-text-secondary text-xs whitespace-nowrap">
                      {d.toLocaleDateString("en-GB", { day: "2-digit", month: "short", year: "numeric" })}
                    </td>
                    <td className="px-3 py-2">
                      {tx.ticker ? (
                        <Link href={`/ticker/${encodeURIComponent(tx.ticker)}`} className="font-mono font-semibold text-accent hover:underline text-xs">
                          {tx.ticker}
                        </Link>
                      ) : <span className="text-text-tertiary">--</span>}
                    </td>
                    <td className="px-3 py-2">
                      <span className={cn("inline-block px-1.5 py-0.5 rounded text-[9px] font-mono font-medium uppercase", TX_TYPE_COLORS[tx.type] || "bg-bg-tertiary text-text-tertiary")}>
                        {tx.type}
                      </span>
                    </td>
                    <td className="px-3 py-2 text-right font-mono text-text-secondary text-xs">
                      {tx.quantity ? parseFloat(tx.quantity).toFixed(parseFloat(tx.quantity) < 1 ? 6 : 2) : "--"}
                    </td>
                    <td className="px-3 py-2 text-right font-mono text-text-secondary text-xs">
                      {tx.unitPrice ? `${tx.currency} ${parseFloat(tx.unitPrice).toFixed(2)}` : "--"}
                    </td>
                    <td className="px-3 py-2 text-right font-mono font-medium text-text-primary text-xs">
                      {tx.currency} {parseFloat(tx.totalAmount).toFixed(2)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between px-3 py-2 border-t border-border-subtle">
          <button
            onClick={() => setPage((p) => Math.max(0, p - 1))}
            disabled={page === 0}
            className="flex items-center gap-1 px-2 py-1 text-xs font-mono text-text-secondary hover:text-text-primary disabled:opacity-30 disabled:cursor-not-allowed"
          >
            <ChevronLeft size={14} /> Prev
          </button>
          <span className="text-xs font-mono text-text-tertiary">
            Page {page + 1} of {totalPages}
          </span>
          <button
            onClick={() => setPage((p) => Math.min(totalPages - 1, p + 1))}
            disabled={page >= totalPages - 1}
            className="flex items-center gap-1 px-2 py-1 text-xs font-mono text-text-secondary hover:text-text-primary disabled:opacity-30 disabled:cursor-not-allowed"
          >
            Next <ChevronRight size={14} />
          </button>
        </div>
      )}
    </div>
  );
}

/* ─── Banking Tab ─── */

type BankingSortKey = "date" | "amount" | "merchant" | "category" | "description";

function BankingTab({ accountId }: { accountId: string }) {
  const [rows, setRows] = useState<BankingTxRow[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(0);
  const [search, setSearch] = useState("");
  const [sortBy, setSortBy] = useState<BankingSortKey>("date");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc");
  const limit = 50;

  function toggleSort(key: BankingSortKey) {
    if (sortBy === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
      return;
    }
    setSortBy(key);
    setSortDir(key === "date" ? "desc" : "asc");
  }

  function header(key: BankingSortKey, label: string, className: string) {
    const active = sortBy === key;
    return (
      <th className={className}>
        <button
          onClick={() => toggleSort(key)}
          className="inline-flex items-center gap-1 hover:text-text-secondary transition-colors"
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
      accountId,
      limit: limit.toString(),
      offset: (page * limit).toString(),
      sortBy,
      sortDir,
    });
    if (search) params.set("search", search);

    const res = await fetch(`/api/banking/transactions?${params}`);
    const json = await res.json();
    setRows(json.data);
    setTotal(json.total);
    setLoading(false);
  }, [accountId, page, search, sortBy, sortDir]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  useEffect(() => {
    setPage(0);
  }, [search, sortBy, sortDir, accountId]);

  const totalPages = Math.ceil(total / limit);

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3 px-1">
        <div className="relative flex-1 max-w-xs">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary" />
          <input
            type="text"
            placeholder="Search description or merchant..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full pl-9 pr-3 py-1.5 bg-bg-tertiary border border-border-subtle rounded-lg text-xs text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent font-mono"
          />
        </div>
        <span className="text-[11px] font-mono text-text-tertiary ml-auto">{total} transactions</span>
      </div>

      {loading ? (
        <div className="py-12"><LoadingSpinner /></div>
      ) : rows.length === 0 ? (
        <div className="py-12 text-center text-sm text-text-tertiary">No banking transactions found</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-[11px] text-text-tertiary uppercase tracking-wider font-mono border-b border-border-subtle">
                {header("date", "Date", "px-3 py-2 font-medium")}
                {header("description", "Description", "px-3 py-2 font-medium")}
                {header("merchant", "Merchant", "px-3 py-2 font-medium")}
                {header("amount", "Amount", "px-3 py-2 font-medium text-right")}
                {header("category", "Category", "px-3 py-2 font-medium")}
                <th className="px-3 py-2 font-medium">Status</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border-subtle">
              {rows.map((r) => {
                const tx = r.transaction;
                const d = new Date(tx.date);
                const amount = parseFloat(tx.amount);
                return (
                  <tr key={tx.id} className="hover:bg-bg-hover transition-colors">
                    <td className="px-3 py-2 font-mono text-text-secondary text-xs whitespace-nowrap">
                      {d.toLocaleDateString("en-GB", { day: "2-digit", month: "short", year: "numeric" })}
                    </td>
                    <td className="px-3 py-2 text-text-secondary text-xs max-w-[250px] truncate" title={tx.description}>
                      {tx.description}
                    </td>
                    <td className="px-3 py-2 font-mono text-text-secondary text-xs">
                      {tx.merchant || <span className="text-text-tertiary">--</span>}
                    </td>
                    <td className={cn("px-3 py-2 text-right font-mono font-medium text-xs whitespace-nowrap", pnlColor(amount))}>
                      {formatMoney(amount, tx.currency)}
                    </td>
                    <td className="px-3 py-2">
                      {tx.category ? (
                        <span className="inline-block px-1.5 py-0.5 rounded text-[9px] font-mono bg-bg-tertiary text-text-secondary truncate max-w-[150px]">
                          {tx.category}
                        </span>
                      ) : <span className="text-text-tertiary text-xs">--</span>}
                    </td>
                    <td className="px-3 py-2">
                      <span className={cn("inline-block px-1.5 py-0.5 rounded text-[9px] font-mono font-medium uppercase", STATUS_COLORS[tx.status] || "bg-bg-tertiary text-text-tertiary")}>
                        {tx.status}
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between px-3 py-2 border-t border-border-subtle">
          <button
            onClick={() => setPage((p) => Math.max(0, p - 1))}
            disabled={page === 0}
            className="flex items-center gap-1 px-2 py-1 text-xs font-mono text-text-secondary hover:text-text-primary disabled:opacity-30 disabled:cursor-not-allowed"
          >
            <ChevronLeft size={14} /> Prev
          </button>
          <span className="text-xs font-mono text-text-tertiary">
            Page {page + 1} of {totalPages}
          </span>
          <button
            onClick={() => setPage((p) => Math.min(totalPages - 1, p + 1))}
            disabled={page >= totalPages - 1}
            className="flex items-center gap-1 px-2 py-1 text-xs font-mono text-text-secondary hover:text-text-primary disabled:opacity-30 disabled:cursor-not-allowed"
          >
            Next <ChevronRight size={14} />
          </button>
        </div>
      )}
    </div>
  );
}

/* ─── Account Detail Panel ─── */

function AccountDetail({ account }: { account: AccountRow }) {
  const [tab, setTab] = useState<"trades" | "banking">(
    account.type === "investment" ? "trades" : "banking"
  );

  const created = new Date(account.createdAt);

  return (
    <div className="space-y-4">
      {/* Header */}
      <div>
        <div className="flex items-center gap-2">
          <h3 className="text-lg font-semibold text-text-primary">{account.name}</h3>
          <span
            className={cn(
              "inline-block px-2 py-0.5 rounded text-[10px] font-mono font-medium uppercase",
              TYPE_COLORS[account.type] || "bg-bg-tertiary text-text-tertiary"
            )}
          >
            {account.type}
          </span>
        </div>
        <div className="flex items-center gap-3 mt-1">
          <span className="text-xs font-mono text-text-tertiary">{account.broker}</span>
          <span className="text-xs text-text-tertiary">·</span>
          <span className="text-xs font-mono text-text-tertiary">{account.baseCurrency}</span>
          <span className="text-xs text-text-tertiary">·</span>
          <span className="text-xs font-mono text-text-tertiary">
            Created {created.toLocaleDateString("en-GB", { day: "2-digit", month: "short", year: "numeric" })}
          </span>
        </div>
      </div>

      {/* Stats */}
      <div className="flex gap-4">
        <div className="px-3 py-2 bg-bg-tertiary rounded-lg">
          <div className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider">Trades</div>
          <div className="text-sm font-mono font-medium text-text-primary">{account.investmentCount}</div>
        </div>
        <div className="px-3 py-2 bg-bg-tertiary rounded-lg">
          <div className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider">Banking Tx</div>
          <div className="text-sm font-mono font-medium text-text-primary">{account.bankingCount}</div>
        </div>
        <div className="px-3 py-2 bg-bg-tertiary rounded-lg">
          <div className="text-[10px] font-mono text-text-tertiary uppercase tracking-wider">Total</div>
          <div className="text-sm font-mono font-medium text-text-primary">
            {account.investmentCount + account.bankingCount}
          </div>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-1 bg-bg-tertiary rounded-lg p-0.5 border border-border-subtle w-fit">
        <button
          onClick={() => setTab("trades")}
          className={cn(
            "flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono font-medium rounded-md transition-all",
            tab === "trades"
              ? "bg-accent text-bg-primary"
              : "text-text-tertiary hover:text-text-secondary"
          )}
        >
          <ArrowLeftRight size={12} /> Trades
        </button>
        <button
          onClick={() => setTab("banking")}
          className={cn(
            "flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono font-medium rounded-md transition-all",
            tab === "banking"
              ? "bg-accent text-bg-primary"
              : "text-text-tertiary hover:text-text-secondary"
          )}
        >
          <Wallet size={12} /> Banking
        </button>
      </div>

      {/* Tab Content */}
      <Card>
        {tab === "trades" ? (
          <TradesTab accountId={account.id} />
        ) : (
          <BankingTab accountId={account.id} />
        )}
      </Card>
    </div>
  );
}

/* ─── Main Page ─── */

export default function AccountsPage() {
  const [accounts, setAccounts] = useState<AccountRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const fetchAccounts = useCallback(async () => {
    const res = await fetch("/api/accounts");
    const data = await res.json();
    setAccounts(data);
    setLoading(false);
  }, []);

  useEffect(() => {
    fetchAccounts();
  }, [fetchAccounts]);

  const selected = accounts.find((a) => a.id === selectedId) || null;

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold tracking-tight">Accounts</h2>
        <p className="text-sm text-text-tertiary mt-0.5">
          Manage accounts and browse their transactions
        </p>
      </div>

      <div className="flex gap-6 items-start">
        {/* Left panel — account list */}
        <div className="w-80 shrink-0">
          <Card>
            <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
              <div className="flex items-center gap-1.5">
                <Building2 size={14} className="text-text-tertiary" />
                <span className="text-xs font-mono text-text-tertiary uppercase tracking-wider">
                  {accounts.length} accounts
                </span>
              </div>
            </div>

            {loading ? (
              <div className="py-12"><LoadingSpinner /></div>
            ) : (
              <div>
                {accounts.map((a) => (
                  <AccountItem
                    key={a.id}
                    account={a}
                    selected={selectedId === a.id}
                    onSelect={() => setSelectedId(a.id)}
                    onRenamed={fetchAccounts}
                    onDeleted={() => {
                      setSelectedId(null);
                      fetchAccounts();
                    }}
                  />
                ))}
                {accounts.length === 0 && (
                  <div className="py-8 text-center text-sm text-text-tertiary">
                    No accounts yet
                  </div>
                )}
              </div>
            )}

            <div className="px-4 py-3 border-t border-border-subtle">
              <CreateAccountForm onCreated={fetchAccounts} />
            </div>
          </Card>
        </div>

        {/* Right panel — account detail */}
        <div className="flex-1 min-w-0">
          {selected ? (
            <AccountDetail key={selected.id} account={selected} />
          ) : (
            <div className="flex items-center justify-center py-20">
              <p className="text-sm text-text-tertiary">Select an account to view details</p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
