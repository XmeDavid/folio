"use client";

import { useState, useEffect, useCallback } from "react";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { Upload, Plus, CheckCircle, AlertCircle } from "lucide-react";

interface Account {
  id: string;
  name: string;
  broker: string;
}

function FileImport() {
  const [broker, setBroker] = useState("Revolut");
  const [accountName, setAccountName] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [status, setStatus] = useState<{
    type: "idle" | "loading" | "success" | "error";
    message: string;
  }>({ type: "idle", message: "" });

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!file) return;

    setStatus({ type: "loading", message: "Importing..." });

    const form = new FormData();
    form.set("file", file);
    form.set("broker", broker);
    form.set("accountName", accountName || `${broker} Account`);

    try {
      const res = await fetch("/api/import", { method: "POST", body: form });
      const json = await res.json();
      if (!res.ok) throw new Error(json.error || "Import failed");
      const imported = json.transactionsImported ?? 0;
      const skipped = json.duplicatesSkipped ?? 0;
      const total = json.totalParsed ?? imported + skipped;
      setStatus({
        type: "success",
        message: `Imported ${imported} / ${total} (skipped ${skipped} duplicates)`,
      });
      setFile(null);
    } catch (err) {
      setStatus({ type: "error", message: String(err) });
    }
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <Upload size={16} className="text-accent" />
          <CardTitle>Import from File</CardTitle>
        </div>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1.5">
                Broker
              </label>
              <select
                value={broker}
                onChange={(e) => setBroker(e.target.value)}
                className="w-full bg-bg-tertiary border border-border-subtle rounded-lg px-3 py-2 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent font-mono"
              >
                <option value="Revolut">Revolut Trading (CSV)</option>
                <option value="IBKR">Interactive Brokers (Activity CSV / JSON)</option>
                <option value="Revolut-Banking">Revolut Account Statement (CSV)</option>
                <option value="Revolut-Savings">Revolut Savings Statement (CSV)</option>
                <option value="PostFinance">PostFinance (CSV)</option>
              </select>
            </div>
            <div>
              <label className="block text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1.5">
                Account Name
              </label>
              <input
                type="text"
                value={accountName}
                onChange={(e) => setAccountName(e.target.value)}
                placeholder="My Trading Account"
                className="w-full bg-bg-tertiary border border-border-subtle rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent"
              />
            </div>
          </div>

          <div>
            <label className="block text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1.5">
              File
            </label>
            <div className="relative">
              <input
                type="file"
                accept=".csv,.json"
                onChange={(e) => setFile(e.target.files?.[0] || null)}
                className="w-full bg-bg-tertiary border border-border-subtle border-dashed rounded-lg px-3 py-4 text-sm text-text-secondary focus:outline-none focus:ring-1 focus:ring-accent file:mr-3 file:bg-accent file:text-bg-primary file:border-0 file:rounded-md file:px-3 file:py-1 file:text-xs file:font-medium file:cursor-pointer"
              />
            </div>
            {broker === "IBKR" && (
              <p className="text-[11px] font-mono text-text-tertiary mt-1.5">
                Use Activity Statement CSV export (or legacy JSON).
              </p>
            )}
            {broker === "Revolut-Banking" && (
              <p className="text-[11px] font-mono text-text-tertiary mt-1.5">
                Revolut account statement CSV. Creates Current + Savings accounts. Skips investment/deposit rows.
              </p>
            )}
            {broker === "Revolut-Savings" && (
              <p className="text-[11px] font-mono text-text-tertiary mt-1.5">
                Revolut savings (money market) statement. Imports interest/fees as banking + BUY/SELL as investment transactions.
              </p>
            )}
            {broker === "PostFinance" && (
              <p className="text-[11px] font-mono text-text-tertiary mt-1.5">
                PostFinance export (semicolon-delimited). Categories are imported directly.
              </p>
            )}
          </div>

          <div className="flex items-center gap-4">
            <button
              type="submit"
              disabled={!file || status.type === "loading"}
              className="px-5 py-2.5 bg-accent text-bg-primary rounded-lg text-sm font-medium hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity"
            >
              {status.type === "loading" ? "Importing..." : "Import"}
            </button>

            {status.type === "success" && (
              <div className="flex items-center gap-1.5 text-green text-sm">
                <CheckCircle size={14} />
                <span className="font-mono">{status.message}</span>
              </div>
            )}
            {status.type === "error" && (
              <div className="flex items-center gap-1.5 text-red text-sm">
                <AlertCircle size={14} />
                <span className="font-mono">{status.message}</span>
              </div>
            )}
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

function ManualEntry() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [form, setForm] = useState({
    accountId: "",
    date: new Date().toISOString().split("T")[0],
    ticker: "",
    type: "BUY",
    quantity: "",
    unitPrice: "",
    totalAmount: "",
    currency: "USD",
    commission: "0",
  });
  const [status, setStatus] = useState<{
    type: "idle" | "loading" | "success" | "error";
    message: string;
  }>({ type: "idle", message: "" });

  const fetchAccounts = useCallback(async () => {
    const res = await fetch("/api/accounts");
    const data = await res.json();
    setAccounts(data);
    if (data.length > 0 && !form.accountId) {
      setForm((f) => ({ ...f, accountId: data[0].id }));
    }
  }, []);

  useEffect(() => {
    fetchAccounts();
  }, [fetchAccounts]);

  function update(field: string, value: string) {
    setForm((f) => ({ ...f, [field]: value }));
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setStatus({ type: "loading", message: "" });
    try {
      const res = await fetch("/api/transactions", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          ...form,
          quantity: form.quantity || null,
          unitPrice: form.unitPrice || null,
        }),
      });
      if (!res.ok) {
        const err = await res.json();
        throw new Error(err.error || "Failed to create");
      }
      setStatus({ type: "success", message: "Transaction added" });
      setForm((f) => ({
        ...f,
        ticker: "",
        quantity: "",
        unitPrice: "",
        totalAmount: "",
        commission: "0",
      }));
    } catch (err) {
      setStatus({ type: "error", message: String(err) });
    }
  }

  const inputClass =
    "w-full bg-bg-tertiary border border-border-subtle rounded-lg px-3 py-2 text-sm text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent font-mono";
  const labelClass =
    "block text-[11px] font-mono text-text-tertiary uppercase tracking-wider mb-1.5";

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <Plus size={16} className="text-green" />
          <CardTitle>Manual Entry</CardTitle>
        </div>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <div>
              <label className={labelClass}>Account</label>
              <select
                value={form.accountId}
                onChange={(e) => update("accountId", e.target.value)}
                className={inputClass}
              >
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name} ({a.broker})
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className={labelClass}>Date</label>
              <input
                type="date"
                value={form.date}
                onChange={(e) => update("date", e.target.value)}
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass}>Type</label>
              <select
                value={form.type}
                onChange={(e) => update("type", e.target.value)}
                className={inputClass}
              >
                <option value="BUY">Buy</option>
                <option value="SELL">Sell</option>
                <option value="DIVIDEND">Dividend</option>
              </select>
            </div>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
            <div>
              <label className={labelClass}>Ticker</label>
              <input
                type="text"
                value={form.ticker}
                onChange={(e) => update("ticker", e.target.value.toUpperCase())}
                placeholder="AAPL"
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass}>Quantity</label>
              <input
                type="number"
                step="any"
                value={form.quantity}
                onChange={(e) => update("quantity", e.target.value)}
                placeholder="10"
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass}>Unit Price</label>
              <input
                type="number"
                step="any"
                value={form.unitPrice}
                onChange={(e) => update("unitPrice", e.target.value)}
                placeholder="150.00"
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass}>Total Amount</label>
              <input
                type="number"
                step="any"
                value={form.totalAmount}
                onChange={(e) => update("totalAmount", e.target.value)}
                placeholder="1500.00"
                required
                className={inputClass}
              />
            </div>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Currency</label>
              <select
                value={form.currency}
                onChange={(e) => update("currency", e.target.value)}
                className={inputClass}
              >
                <option value="USD">USD</option>
                <option value="EUR">EUR</option>
                <option value="CHF">CHF</option>
              </select>
            </div>
            <div>
              <label className={labelClass}>Commission</label>
              <input
                type="number"
                step="any"
                value={form.commission}
                onChange={(e) => update("commission", e.target.value)}
                className={inputClass}
              />
            </div>
          </div>

          <div className="flex items-center gap-4">
            <button
              type="submit"
              disabled={status.type === "loading"}
              className="px-5 py-2.5 bg-green text-bg-primary rounded-lg text-sm font-medium hover:opacity-90 disabled:opacity-40 transition-opacity"
            >
              Add Transaction
            </button>
            {status.type === "success" && (
              <div className="flex items-center gap-1.5 text-green text-sm font-mono">
                <CheckCircle size={14} />
                {status.message}
              </div>
            )}
            {status.type === "error" && (
              <div className="flex items-center gap-1.5 text-red text-sm font-mono">
                <AlertCircle size={14} />
                {status.message}
              </div>
            )}
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

export default function ImportPage() {
  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold tracking-tight">Import</h2>
        <p className="text-sm text-text-tertiary mt-0.5">
          Import transactions from brokers or add manually
        </p>
      </div>
      <FileImport />
      <ManualEntry />
    </div>
  );
}
