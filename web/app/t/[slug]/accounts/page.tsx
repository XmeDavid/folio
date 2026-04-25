"use client";

import * as React from "react";
import { use } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  ArchiveRestore,
  Check,
  FileUp,
  Pencil,
  Plus,
  Trash2,
  X,
} from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { CreateAccountForm } from "@/components/accounts/create-account-form";
import {
  ApiError,
  applyAccountImportPlan,
  deleteAccount,
  fetchAccounts,
  previewAccountImport,
  updateAccount,
  type Account,
  type AccountKind,
  type ImportCurrencyGroup,
  type ImportPlanGroup,
  type ImportPreview,
} from "@/lib/api/client";
import { useCurrentTenant } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";
import { ACCOUNT_KINDS, accountKindLabel } from "@/lib/accounts";

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
  const [includeArchived, setIncludeArchived] = React.useState(false);

  const accountsQuery = useQuery({
    queryKey: ["accounts", tenantId, includeArchived],
    queryFn: () => fetchAccounts(tenantId!, { includeArchived }),
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

      {importing ? (
        <Card>
          <CardHeader>
            <CardTitle>Import bank export</CardTitle>
          </CardHeader>
          <CardContent>
            <SmartAccountImport
              tenantId={tenantId!}
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
        <LoadingText />
      ) : accountsQuery.data && accountsQuery.data.length > 0 ? (
        <div className="flex flex-col gap-2">
          <label className="flex items-center gap-2 self-end text-[12px] text-fg-muted">
            <input
              type="checkbox"
              className="h-3.5 w-3.5"
              checked={includeArchived}
              onChange={(e) => setIncludeArchived(e.target.checked)}
            />
            Show archived
          </label>
          <AccountList
            accounts={accountsQuery.data}
            locale={locale}
            tenantId={tenant.id}
          />
        </div>
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

function SmartAccountImport({
  tenantId,
  onDone,
}: {
  tenantId: string;
  onDone: () => void;
}) {
  const [preview, setPreview] = React.useState<ImportPreview | null>(null);
  const [plans, setPlans] = React.useState<Record<string, ImportPlanGroup>>({});
  const groups = preview?.currencyGroups?.length
    ? preview.currencyGroups
    : preview
      ? [
          {
            currency: preview.suggestedCurrency ?? "",
            suggestedName: preview.suggestedName ?? "Imported account",
            suggestedKind: preview.suggestedKind ?? "checking",
            suggestedOpenDate: preview.suggestedOpenDate,
            transactionCount: preview.transactionCount,
            dateFrom: preview.dateFrom,
            dateTo: preview.dateTo,
            action: "create_account" as const,
            importableCount: preview.importableCount,
            duplicateCount: preview.duplicateCount,
            conflictCount: preview.conflictCount,
            sampleTransactions: preview.sampleTransactions,
            conflictTransactions: preview.conflictTransactions,
          },
        ]
      : [];

  const previewMutation = useMutation({
    mutationFn: (file: File) => previewAccountImport(tenantId, file),
    onSuccess: (p) => {
      setPreview(p);
      const next: Record<string, ImportPlanGroup> = {};
      const previewGroups = p.currencyGroups?.length
        ? p.currencyGroups
        : [
            {
              currency: p.suggestedCurrency ?? "",
              suggestedName: p.suggestedName ?? "Imported account",
              suggestedKind: p.suggestedKind ?? "checking",
              suggestedOpenDate: p.suggestedOpenDate,
              dateFrom: p.dateFrom,
              existingAccountId: p.existingAccountId,
              action: p.existingAccountId ? "import_to_account" : "create_account",
            } as ImportCurrencyGroup,
          ];
      for (const group of previewGroups) {
        next[importGroupKey(group)] = {
          currency: group.currency,
          sourceKey: group.sourceKey,
          action: group.existingAccountId ? "import_to_account" : "create_account",
          accountId: group.existingAccountId,
          name: group.suggestedName,
          kind: group.suggestedKind,
          openDate: group.suggestedOpenDate ?? group.dateFrom,
          openingBalance: "0",
          openingBalanceDate: group.suggestedOpenDate ?? group.dateFrom,
        };
      }
      setPlans(next);
    },
  });
  const applyMutation = useMutation({
    mutationFn: async () => {
      if (!preview) return;
      await applyAccountImportPlan(
        tenantId,
        preview.fileToken,
        groups
          .filter((group) => effectiveImportable(group, plans[importGroupKey(group)]) > 0)
          .map((group) => {
            const plan = plans[importGroupKey(group)];
            return plan && plan.action === "create_account"
              ? { ...plan, openingBalanceDate: plan.openingBalanceDate || plan.openDate }
              : plan;
          })
          .filter((plan): plan is ImportPlanGroup => Boolean(plan)),
      );
    },
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
        <div className="rounded-[12px] border border-border bg-surface px-4 py-3">
          <p className="text-[13px] font-medium">Smart import</p>
          <p className="mt-1 text-[12px] text-fg-muted">
            Folio will match clear existing accounts or create separate accounts per currency.
          </p>
        </div>
        <label className="flex flex-col gap-1.5 text-[13px] font-medium">
          Export file
          <Input
            type="file"
            accept=".csv,text/csv"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) previewMutation.mutate(file);
            }}
          />
        </label>
      </div>

      {previewMutation.isPending ? (
        <p className="text-[13px] text-fg-muted">Reading export...</p>
      ) : null}

      {preview ? (
        <div className="rounded-[12px] border border-border bg-surface px-4 py-3">
          <p className="text-[13px] font-medium">
            {preview.fileName || "Export"} · {preview.dateFrom} to{" "}
            {preview.dateTo}
          </p>
          {!preview.currencyGroups?.length ? (
            <div className="mt-2 grid gap-2 text-[12px] text-fg-muted sm:grid-cols-4">
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
          ) : null}
          {preview.conflictTransactions?.length ? (
            <div className="mt-2 space-y-1 text-[12px] text-amber">
              <p>
                {preview.conflictTransactions.length} row{preview.conflictTransactions.length === 1 ? "" : "s"} need review and will not be imported automatically.
              </p>
              {(() => {
                const driftCount = preview.conflictTransactions.filter((c) => c.reason === "date_drift").length;
                const descCount = preview.conflictTransactions.filter((c) => c.reason === "description_mismatch").length;
                return (
                  <ul className="list-disc pl-4 text-fg-muted">
                    {driftCount > 0 ? (
                      <li>
                        {driftCount} possible duplicate{driftCount === 1 ? "" : "s"} with different dates (within ±7 days)
                      </li>
                    ) : null}
                    {descCount > 0 ? (
                      <li>
                        {descCount} same amount/date with a different description
                      </li>
                    ) : null}
                  </ul>
                );
              })()}
            </div>
          ) : null}
          {preview.warnings?.length ? (
            <ul className="mt-2 list-disc pl-4 text-[12px] text-fg-muted">
              {preview.warnings.map((warning) => (
                <li key={warning}>{warning}</li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      {groups.length ? (
        <div className="overflow-hidden rounded-[12px] border border-border">
          <div className="grid grid-cols-[0.7fr_1.4fr_0.9fr_0.8fr] gap-3 border-b border-border bg-surface-subtle px-4 py-2 text-[11px] font-medium uppercase tracking-[0.04em] text-fg-muted">
            <span>Currency</span>
            <span>Account</span>
            <span>Range</span>
            <span className="text-right">Rows</span>
          </div>
          <div className="divide-y divide-border bg-surface">
            {groups.map((group) => {
              const key = importGroupKey(group);
              return (
                <ImportGroupRow
                  key={key}
                  group={group}
                  plan={plans[key]}
                  onPlanChange={(plan) =>
                    setPlans((current) => ({
                      ...current,
                      [key]: plan,
                    }))
                  }
                />
              );
            })}
          </div>
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
          disabled={
            !preview ||
            applyMutation.isPending ||
            groups.every((group) => effectiveImportable(group, plans[importGroupKey(group)]) === 0) ||
            groups.some((group) => !isImportPlanReady(group, plans[importGroupKey(group)]))
          }
          onClick={() => applyMutation.mutate()}
        >
          {applyMutation.isPending ? "Importing..." : "Apply import plan"}
        </Button>
      </div>
    </div>
  );
}

function ImportGroupRow({
  group,
  plan,
  onPlanChange,
}: {
  group: ImportCurrencyGroup;
  plan?: ImportPlanGroup;
  onPlanChange: (plan: ImportPlanGroup) => void;
}) {
  if (!plan) return null;
  const candidates = group.candidateAccounts ?? [];
  const selectedCandidate =
    plan.action === "import_to_account" && plan.accountId
      ? candidates.find((c) => c.id === plan.accountId)
      : undefined;
  const counts = selectedCandidate
    ? {
        importable: selectedCandidate.importableCount,
        duplicates: selectedCandidate.duplicateCount,
        conflicts: selectedCandidate.conflictCount,
      }
    : {
        importable: group.importableCount,
        duplicates: group.duplicateCount,
        conflicts: group.conflictCount,
      };
  const set = (patch: Partial<ImportPlanGroup>) =>
    onPlanChange({ ...plan, ...patch });
  return (
    <div className="grid gap-3 px-4 py-3 text-[13px] lg:grid-cols-[0.55fr_1.4fr_1fr_0.9fr]">
      <div>
        <span className="font-medium tabular text-fg">{group.currency}</span>
        {group.sourceKey ? (
          <p className="mt-1 truncate text-[12px] font-medium text-fg" title={group.sourceKey}>
            {group.sourceKey}
          </p>
        ) : null}
        <p className="mt-1 text-[12px] text-fg-muted">
          {group.dateFrom || "-"} to {group.dateTo || "-"}
        </p>
      </div>
      <div className="grid gap-2">
        <select
          className="h-9 rounded-[8px] border border-border bg-surface px-3"
          value={plan.action === "import_to_account" ? plan.accountId ?? "" : "create"}
          onChange={(e) => {
            if (e.target.value === "create") {
              set({ action: "create_account", accountId: undefined });
            } else {
              set({ action: "import_to_account", accountId: e.target.value });
            }
          }}
        >
          <option value="create">Create account</option>
          {candidates.map((account) => (
            <option key={account.id} value={account.id}>
              Import to {account.name}
              {account.institution ? ` (${account.institution})` : ""}
              {account.archived ? " — archived" : ""}
            </option>
          ))}
        </select>
        {plan.action === "create_account" ? (
          <Input
            value={plan.name ?? ""}
            onChange={(e) => set({ name: e.target.value })}
            placeholder="Account name"
          />
        ) : null}
      </div>
      {plan.action === "create_account" ? (
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-1">
          <select
            className="h-9 rounded-[8px] border border-border bg-surface px-3"
            value={plan.kind}
            onChange={(e) => set({ kind: e.target.value as AccountKind })}
          >
            {ACCOUNT_KINDS.map((kind) => (
              <option key={kind.value} value={kind.value}>
                {kind.label}
              </option>
            ))}
          </select>
          <Input
            type="date"
            value={plan.openDate ?? ""}
            onChange={(e) =>
              set({
                openDate: e.target.value,
                openingBalanceDate: e.target.value,
              })
            }
          />
          <Input
            inputMode="decimal"
            value={plan.openingBalance ?? ""}
            onChange={(e) => set({ openingBalance: e.target.value })}
            placeholder="Opening balance"
          />
        </div>
      ) : selectedCandidate?.archived ? (
        <label className="flex items-start gap-2 text-[12px] text-fg-muted">
          <input
            type="checkbox"
            className="mt-[3px]"
            checked={!!plan.reactivate}
            onChange={(e) => set({ reactivate: e.target.checked })}
          />
          <span>
            Archived account. Transactions will import either way; tick to also
            reactivate it.
          </span>
        </label>
      ) : (
        <div className="text-[12px] text-fg-muted">Existing account</div>
      )}
      <div className="text-right text-[12px] text-fg-muted">
        <strong className="tabular text-fg">{counts.importable}</strong> new
        {counts.duplicates ? ` · ${counts.duplicates} dupes` : ""}
        {counts.conflicts ? ` · ${counts.conflicts} review` : ""}
      </div>
    </div>
  );
}

const decimalRe = /^-?\d+(\.\d+)?$/;

function importGroupKey(group: { currency: string; sourceKey?: string }): string {
  return `${group.currency}|${group.sourceKey ?? ""}`;
}

function effectiveImportable(group: ImportCurrencyGroup, plan?: ImportPlanGroup) {
  if (plan?.action === "import_to_account" && plan.accountId) {
    const match = group.candidateAccounts?.find((c) => c.id === plan.accountId);
    if (match) return match.importableCount;
  }
  return group.importableCount;
}

function isImportPlanReady(group: ImportCurrencyGroup, plan?: ImportPlanGroup) {
  if (effectiveImportable(group, plan) === 0) return true;
  if (!plan) return false;
  if (plan.action === "import_to_account") return !!plan.accountId;
  return !!(
    plan.name?.trim() &&
    plan.kind &&
    plan.openDate &&
    plan.openingBalance?.trim() &&
    decimalRe.test(plan.openingBalance.trim())
  );
}

function AccountList({
  accounts,
  locale,
  tenantId,
}: {
  accounts: Account[];
  locale?: string;
  tenantId: string;
}) {
  return (
    <Card className="overflow-hidden">
      <ul className="divide-y divide-border">
        {accounts.map((a) => (
          <AccountRow
            key={a.id}
            account={a}
            locale={locale}
            tenantId={tenantId}
          />
        ))}
      </ul>
    </Card>
  );
}

function AccountRow({
  account,
  locale,
  tenantId,
}: {
  account: Account;
  locale?: string;
  tenantId: string;
}) {
  const queryClient = useQueryClient();
  const [mode, setMode] = React.useState<"view" | "edit" | "confirm-delete">("view");
  const [draftName, setDraftName] = React.useState(account.name);
  const [draftKind, setDraftKind] = React.useState<AccountKind>(account.kind);

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["accounts", tenantId] });

  const editMutation = useMutation({
    mutationFn: (patch: { name?: string; kind?: AccountKind }) =>
      updateAccount(tenantId, account.id, patch),
    onSuccess: async () => {
      await invalidate();
      setMode("view");
    },
  });

  const archiveMutation = useMutation({
    mutationFn: (archived: boolean) =>
      updateAccount(tenantId, account.id, { archived }),
    onSuccess: invalidate,
  });

  const deleteMutation = useMutation({
    mutationFn: () => deleteAccount(tenantId, account.id),
    onSuccess: invalidate,
  });

  const busy =
    editMutation.isPending ||
    archiveMutation.isPending ||
    deleteMutation.isPending;
  const apiError =
    (editMutation.error instanceof ApiError && editMutation.error) ||
    (archiveMutation.error instanceof ApiError && archiveMutation.error) ||
    (deleteMutation.error instanceof ApiError && deleteMutation.error) ||
    null;

  const archived = !!account.archivedAt;

  return (
    <li className="flex flex-col gap-3 px-5 py-4 transition-colors hover:bg-surface-subtle sm:flex-row sm:items-start sm:justify-between">
      <div className="flex min-w-0 flex-col gap-1.5">
        {mode === "edit" ? (
          <div className="flex flex-wrap items-center gap-2">
            <Input
              value={draftName}
              onChange={(e) => setDraftName(e.target.value)}
              autoFocus
              className="h-8 max-w-[280px]"
              placeholder="Account name"
            />
            <select
              value={draftKind}
              onChange={(e) => setDraftKind(e.target.value as AccountKind)}
              className="h-8 rounded-[8px] border border-border bg-surface px-2 text-[13px]"
            >
              {ACCOUNT_KINDS.map((k) => (
                <option key={k.value} value={k.value}>
                  {k.label}
                </option>
              ))}
            </select>
          </div>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[15px] font-medium text-fg">
              {account.name}
            </span>
            {account.nickname ? (
              <span className="text-[12px] text-fg-faint">
                ({account.nickname})
              </span>
            ) : null}
            <Badge variant="neutral">{accountKindLabel(account.kind)}</Badge>
            {archived ? <Badge variant="amber">Archived</Badge> : null}
          </div>
        )}
        <div className="text-[12px] text-fg-muted">
          {account.currency}
          {account.institution ? `  -  ${account.institution}` : ""} - opened{" "}
          {formatDate(account.openDate, locale)}
        </div>
        {apiError ? (
          <p className="text-[11px] text-danger">
            {apiError.body?.error || apiError.message}
          </p>
        ) : null}
      </div>
      <div className="flex items-center gap-3">
        <div className="flex flex-col items-end">
          <span className="tabular text-[15px] font-medium text-fg">
            {formatAmount(account.balance, account.currency, locale)}
          </span>
          <span className="text-[11px] text-fg-faint">
            {account.balanceAsOf
              ? `as of ${formatDate(account.balanceAsOf, locale)}`
              : "no snapshot yet"}
          </span>
        </div>
        <AccountRowActions
          mode={mode}
          busy={busy}
          archived={archived}
          onEdit={() => {
            setDraftName(account.name);
            setDraftKind(account.kind);
            setMode("edit");
          }}
          onSaveEdit={() => {
            const patch: { name?: string; kind?: AccountKind } = {};
            const trimmed = draftName.trim();
            if (trimmed && trimmed !== account.name) patch.name = trimmed;
            if (draftKind !== account.kind) patch.kind = draftKind;
            if (Object.keys(patch).length === 0) {
              setMode("view");
              return;
            }
            editMutation.mutate(patch);
          }}
          onCancelEdit={() => {
            setDraftName(account.name);
            setDraftKind(account.kind);
            setMode("view");
          }}
          onAskDelete={() => setMode("confirm-delete")}
          onConfirmDelete={() => deleteMutation.mutate()}
          onCancelDelete={() => setMode("view")}
          onArchive={() => archiveMutation.mutate(true)}
          onRestore={() => archiveMutation.mutate(false)}
        />
      </div>
    </li>
  );
}

function AccountRowActions({
  mode,
  busy,
  archived,
  onEdit,
  onSaveEdit,
  onCancelEdit,
  onAskDelete,
  onConfirmDelete,
  onCancelDelete,
  onArchive,
  onRestore,
}: {
  mode: "view" | "edit" | "confirm-delete";
  busy: boolean;
  archived: boolean;
  onEdit: () => void;
  onSaveEdit: () => void;
  onCancelEdit: () => void;
  onAskDelete: () => void;
  onConfirmDelete: () => void;
  onCancelDelete: () => void;
  onArchive: () => void;
  onRestore: () => void;
}) {
  if (mode === "edit") {
    return (
      <div className="flex items-center gap-1">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onSaveEdit}
          disabled={busy}
          aria-label="Save changes"
        >
          <Check className="h-4 w-4" />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onCancelEdit}
          disabled={busy}
          aria-label="Cancel"
        >
          <X className="h-4 w-4" />
        </Button>
      </div>
    );
  }
  if (mode === "confirm-delete") {
    return (
      <div className="flex flex-col items-end gap-1">
        <p className="text-[11px] text-danger">
          Delete account and all its transactions?
        </p>
        <div className="flex items-center gap-1">
          <Button
            type="button"
            variant="danger"
            size="sm"
            onClick={onConfirmDelete}
            disabled={busy}
          >
            {busy ? "Deleting..." : "Delete"}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={onCancelDelete}
            disabled={busy}
          >
            Cancel
          </Button>
        </div>
      </div>
    );
  }
  return (
    <div className="flex items-center gap-1">
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onEdit}
        disabled={busy}
        aria-label="Edit account"
      >
        <Pencil className="h-4 w-4" />
      </Button>
      {archived ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onRestore}
          disabled={busy}
          aria-label="Restore account"
        >
          <ArchiveRestore className="h-4 w-4" />
        </Button>
      ) : (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onArchive}
          disabled={busy}
          aria-label="Archive account"
        >
          <Archive className="h-4 w-4" />
        </Button>
      )}
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onAskDelete}
        disabled={busy}
        aria-label="Delete account"
      >
        <Trash2 className="h-4 w-4" />
      </Button>
    </div>
  );
}
