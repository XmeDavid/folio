"use client";

import * as React from "react";
import { use } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { FileUp, Plus } from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { CreateAccountForm } from "@/components/accounts/create-account-form";
import {
  ApiError,
  applyAccountImport,
  fetchAccounts,
  previewAccountImport,
  type Account,
  type ImportPreview,
} from "@/lib/api/client";
import { useCurrentTenant } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";
import { accountKindLabel } from "@/lib/accounts";

export default function AccountsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const tenant = useCurrentTenant(slug);
  const tenantId = tenant?.id ?? null;
  const [creating, setCreating] = React.useState(false);
  const [importing, setImporting] = React.useState(false);

  const accountsQuery = useQuery({
    queryKey: ["accounts", tenantId],
    queryFn: () => fetchAccounts(tenantId!),
    enabled: !!tenantId,
  });

  if (!tenant) return null;

  const locale = tenant.locale;
  const baseCurrency = tenant.baseCurrency ?? "CHF";

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Ledger"
        title="Accounts"
        description="Every balance in Folio lives on an account. Start with checking or cash; credit cards and liabilities come next."
        actions={
          <div className="flex flex-wrap gap-2">
            <Button
              variant="secondary"
              onClick={() => setImporting((v) => !v)}
              disabled={!accountsQuery.data?.length}
            >
              <FileUp className="h-4 w-4" />
              {importing ? "Close" : "Import"}
            </Button>
            <Button onClick={() => setCreating((v) => !v)}>
              <Plus className="h-4 w-4" />
              {creating ? "Close" : "Add account"}
            </Button>
          </div>
        }
      />

      {creating && tenantId ? (
        <Card>
          <CardHeader>
            <CardTitle>New account</CardTitle>
          </CardHeader>
          <CardContent>
            <CreateAccountForm
              tenantId={tenantId}
              defaultCurrency={baseCurrency}
              onCreated={() => setCreating(false)}
              onCancel={() => setCreating(false)}
            />
          </CardContent>
        </Card>
      ) : null}

      {importing && accountsQuery.data?.length ? (
        <Card>
          <CardHeader>
            <CardTitle>Import transactions</CardTitle>
          </CardHeader>
          <CardContent>
            <ExistingAccountImport
              tenantId={tenantId!}
              accounts={accountsQuery.data}
              onDone={() => setImporting(false)}
            />
          </CardContent>
        </Card>
      ) : null}

      {accountsQuery.isError ? (
        <ErrorBanner
          title="Couldn't load accounts"
          description="Is the backend running on :8080?"
        />
      ) : null}

      {accountsQuery.isLoading ? (
        <p className="text-[13px] text-fg-muted">Loading...</p>
      ) : accountsQuery.data && accountsQuery.data.length > 0 ? (
        <AccountList accounts={accountsQuery.data} locale={locale} />
      ) : (
        <EmptyState
          title="No accounts yet"
          description="Create your first account to bootstrap the ledger. Every transaction must post to an account."
          action={
            <Button onClick={() => setCreating(true)}>
              <Plus className="h-4 w-4" />
              Add account
            </Button>
          }
        />
      )}
    </div>
  );
}

function ExistingAccountImport({
  tenantId,
  accounts,
  onDone,
}: {
  tenantId: string;
  accounts: Account[];
  onDone: () => void;
}) {
  const [accountId, setAccountId] = React.useState(accounts[0]?.id ?? "");
  const [preview, setPreview] = React.useState<ImportPreview | null>(null);

  const previewMutation = useMutation({
    mutationFn: (file: File) => previewAccountImport(tenantId, file, accountId),
    onSuccess: setPreview,
  });
  const applyMutation = useMutation({
    mutationFn: () => applyAccountImport(tenantId, accountId, preview!.fileToken),
    onSuccess: () => onDone(),
  });
  const err =
    previewMutation.error instanceof ApiError
      ? previewMutation.error
      : applyMutation.error instanceof ApiError
        ? applyMutation.error
        : null;

  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <label className="flex flex-col gap-1.5 text-[13px] font-medium">
          Account
          <select
            className="h-9 w-full rounded-[8px] border border-border bg-surface px-3 text-[14px]"
            value={accountId}
            onChange={(e) => {
              setAccountId(e.target.value);
              setPreview(null);
            }}
          >
            {accounts.map((account) => (
              <option key={account.id} value={account.id}>
                {account.name} · {account.currency}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1.5 text-[13px] font-medium">
          Export file
          <Input
            type="file"
            accept=".csv,text/csv"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file && accountId) previewMutation.mutate(file);
            }}
          />
        </label>
      </div>

      {previewMutation.isPending ? (
        <p className="text-[13px] text-[--color-fg-muted]">Reading export...</p>
      ) : null}

      {preview ? (
        <div className="rounded-[12px] border border-[--color-border] bg-[--color-surface] px-4 py-3">
          <p className="text-[13px] font-medium">
            {preview.fileName || "Export"} · {preview.dateFrom} to{" "}
            {preview.dateTo}
          </p>
          <div className="mt-2 grid gap-2 text-[12px] text-[--color-fg-muted] sm:grid-cols-4">
            <span>
              Total: <strong>{preview.transactionCount}</strong>
            </span>
            <span>
              New: <strong>{preview.importableCount}</strong>
            </span>
            <span>
              Duplicates: <strong>{preview.duplicateCount}</strong>
            </span>
            <span>
              Review: <strong>{preview.conflictCount}</strong>
            </span>
          </div>
          {preview.conflictTransactions?.length ? (
            <p className="mt-2 text-[12px] text-[--color-amber]">
              Some rows need review and will not be imported automatically.
            </p>
          ) : null}
        </div>
      ) : null}

      {err ? (
        <div className="rounded-[8px] border border-border bg-[#F5DADA] px-3 py-2 text-[13px] text-danger">
          {err.body?.error || err.message}
        </div>
      ) : null}

      <div className="flex justify-end gap-2">
        <Button type="button" variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button
          type="button"
          disabled={!preview || applyMutation.isPending || preview.importableCount === 0}
          onClick={() => applyMutation.mutate()}
        >
          {applyMutation.isPending ? "Importing..." : "Import new rows"}
        </Button>
      </div>
    </div>
  );
}

function AccountList({
  accounts,
  locale,
}: {
  accounts: Account[];
  locale?: string;
}) {
  return (
    <Card className="overflow-hidden">
      <ul className="divide-y divide-border">
        {accounts.map((a) => (
          <li
            key={a.id}
            className="flex flex-col gap-3 px-5 py-4 transition-colors hover:bg-surface-subtle sm:flex-row sm:items-center sm:justify-between"
          >
            <div className="flex min-w-0 flex-col gap-0.5">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-[15px] font-medium text-fg">
                  {a.name}
                </span>
                {a.nickname ? (
                  <span className="text-[12px] text-fg-faint">
                    ({a.nickname})
                  </span>
                ) : null}
                <Badge variant="neutral">{accountKindLabel(a.kind)}</Badge>
                {a.archivedAt ? <Badge variant="amber">Archived</Badge> : null}
              </div>
              <div className="text-[12px] text-fg-muted">
                {a.currency}
                {a.institution ? `  -  ${a.institution}` : ""} - opened{" "}
                {formatDate(a.openDate, locale)}
              </div>
            </div>
            <div className="flex flex-col items-end">
              <span className="tabular text-[15px] font-medium text-fg">
                {formatAmount(a.balance, a.currency, locale)}
              </span>
              <span className="text-[11px] text-fg-faint">
                {a.balanceAsOf
                  ? `as of ${formatDate(a.balanceAsOf, locale)}`
                  : "no snapshot yet"}
              </span>
            </div>
          </li>
        ))}
      </ul>
    </Card>
  );
}
