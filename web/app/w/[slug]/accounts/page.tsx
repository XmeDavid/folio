"use client";

import * as React from "react";
import { use } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDown,
  ArrowUp,
  Archive,
  ArchiveRestore,
  Calculator,
  Check,
  ChevronDown,
  ChevronRight,
  FileUp,
  FolderPlus,
  GripVertical,
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
import { DateInput } from "@/components/ui/date-input";
import { CreateAccountForm } from "@/components/accounts/create-account-form";
import {
  ApiError,
  applyAccountImportPlan,
  createAccountGroup,
  deleteAccount,
  deleteAccountGroup,
  fetchAccountGroups,
  fetchAccounts,
  previewAccountImport,
  reorderAccounts,
  updateAccount,
  updateAccountGroup,
  type Account,
  type AccountGroup,
  type AccountKind,
  type ImportCurrencyGroup,
  type ImportPlanGroup,
  type ImportPreview,
  type SmartInvestmentImportResult,
} from "@/lib/api/client";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";
import { ACCOUNT_KINDS, accountKindLabel } from "@/lib/accounts";

export default function AccountsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  const workspaceId = workspace?.id ?? null;
  const [creating, setCreating] = React.useState(false);
  const [importing, setImporting] = React.useState(false);
  const [includeArchived, setIncludeArchived] = React.useState(false);

  const accountsQuery = useQuery({
    queryKey: ["accounts", workspaceId, includeArchived],
    queryFn: () => fetchAccounts(workspaceId!, { includeArchived }),
    enabled: !!workspaceId,
  });
  const groupsQuery = useQuery({
    queryKey: ["account-groups", workspaceId],
    queryFn: () => fetchAccountGroups(workspaceId!),
    enabled: !!workspaceId,
  });

  if (!workspace) return null;

  const locale = workspace.locale;
  const baseCurrency = workspace.baseCurrency ?? "CHF";

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Ledger"
        title="Accounts"
        description="Every balance in Folio lives on an account. Start with checking or cash; credit cards and liabilities come next."
        actions={
          <div className="flex flex-wrap gap-2">
            <Button
              onClick={() => {
                setImporting((v) => !v);
                setCreating(false);
              }}
            >
              <FileUp className="h-4 w-4" />
              {importing ? "Close" : "Import"}
            </Button>
            <Button
              variant="secondary"
              onClick={() => {
                setCreating((v) => !v);
                setImporting(false);
              }}
            >
              <Plus className="h-4 w-4" />
              {creating ? "Close" : "Add account"}
            </Button>
          </div>
        }
      />

      {creating && workspaceId ? (
        <Card>
          <CardHeader>
            <CardTitle>New account</CardTitle>
          </CardHeader>
          <CardContent>
            <CreateAccountForm
              workspaceId={workspaceId}
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
              workspaceId={workspaceId!}
              onDone={() => setImporting(false)}
            />
          </CardContent>
        </Card>
      ) : null}

      {accountsQuery.isError || groupsQuery.isError ? (
        <ErrorBanner
          title="Couldn't load accounts"
          description="Is the backend running on :8080?"
        />
      ) : null}

      {accountsQuery.isLoading || groupsQuery.isLoading ? (
        <LoadingText />
      ) : accountsQuery.data && accountsQuery.data.length > 0 ? (
        <div className="flex flex-col gap-2">
          <label className="text-fg-muted flex items-center gap-2 self-end text-[12px]">
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
            groups={groupsQuery.data ?? []}
            locale={locale}
            workspaceId={workspace.id}
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
  workspaceId,
  onDone,
}: {
  workspaceId: string;
  onDone: () => void;
}) {
  const [preview, setPreview] = React.useState<ImportPreview | null>(null);
  const [plans, setPlans] = React.useState<Record<string, ImportPlanGroup>>({});
  const [investmentResult, setInvestmentResult] = React.useState<
    SmartInvestmentImportResult | null
  >(null);
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
    mutationFn: (file: File) => previewAccountImport(workspaceId, file),
    onSuccess: (resp) => {
      if (resp.kind === "investment") {
        setInvestmentResult(resp.investment);
        setPreview(null);
        setPlans({});
        return;
      }
      setInvestmentResult(null);
      const p = resp.preview;
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
              action: p.existingAccountId
                ? "import_to_account"
                : "create_account",
            } as ImportCurrencyGroup,
          ];
      for (const group of previewGroups) {
        next[importGroupKey(group)] = {
          currency: group.currency,
          sourceKey: group.sourceKey,
          action: group.existingAccountId
            ? "import_to_account"
            : "create_account",
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
        workspaceId,
        preview.fileToken,
        groups
          .filter(
            (group) =>
              effectiveImportable(group, plans[importGroupKey(group)]) > 0
          )
          .map((group) => {
            const plan = plans[importGroupKey(group)];
            return plan && plan.action === "create_account"
              ? {
                  ...plan,
                  openingBalanceDate: plan.openingBalanceDate || plan.openDate,
                }
              : plan;
          })
          .filter((plan): plan is ImportPlanGroup => Boolean(plan))
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
        <div className="border-border bg-surface rounded-[12px] border px-4 py-3">
          <p className="text-[13px] font-medium">Smart import</p>
          <p className="text-fg-muted mt-1 text-[12px]">
            Folio will match clear existing accounts or create separate accounts
            per currency.
          </p>
        </div>
        <label className="flex flex-col gap-1.5 text-[13px] font-medium">
          Export file
          <Input
            type="file"
            accept=".csv,.json,.xml,text/csv,application/json,application/xml"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) previewMutation.mutate(file);
            }}
          />
        </label>
      </div>

      {previewMutation.isPending ? (
        <p className="text-fg-muted text-[13px]">Reading export...</p>
      ) : null}

      {investmentResult ? (
        <div className="border-border bg-surface rounded-[12px] border px-4 py-3">
          <p className="text-[13px] font-medium text-fg">
            Investment activity imported into{" "}
            <span className="text-emerald-500">{investmentResult.accountName}</span>
            {investmentResult.created ? " (new account)" : ""}
          </p>
          <p className="text-fg-muted mt-1 text-[12px]">
            Source: {investmentResult.source.replace("_", " ")} · base{" "}
            {investmentResult.baseCurrency}
          </p>
          <ul className="mt-2 grid gap-1 text-[12px] text-fg-muted sm:grid-cols-4">
            <li>
              Trades created:{" "}
              <strong className="text-fg">
                {investmentResult.summary.tradesCreated}
              </strong>
            </li>
            <li>
              Dividends:{" "}
              <strong className="text-fg">
                {investmentResult.summary.dividendsCreated}
              </strong>
            </li>
            <li>
              Instruments:{" "}
              <strong className="text-fg">
                {investmentResult.summary.instrumentsTouched}
              </strong>
            </li>
            <li>
              Skipped (dedupe):{" "}
              <strong className="text-fg">
                {investmentResult.summary.skipped}
              </strong>
            </li>
          </ul>
          {investmentResult.summary.warnings &&
          investmentResult.summary.warnings.length > 0 ? (
            <details className="mt-2">
              <summary className="cursor-pointer text-[12px] text-fg-muted">
                {investmentResult.summary.warnings.length} warning(s)
              </summary>
              <ul className="mt-1 list-disc pl-5 text-[12px] text-fg-muted">
                {investmentResult.summary.warnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            </details>
          ) : null}
          <div className="mt-3 flex justify-end">
            <Button variant="secondary" onClick={onDone}>
              Done
            </Button>
          </div>
        </div>
      ) : null}

      {preview ? (
        <div className="border-border bg-surface rounded-[12px] border px-4 py-3">
          <p className="text-[13px] font-medium">
            {preview.fileName || "Export"} · {preview.dateFrom} to{" "}
            {preview.dateTo}
          </p>
          {!preview.currencyGroups?.length ? (
            <div className="text-fg-muted mt-2 grid gap-2 text-[12px] sm:grid-cols-4">
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
            <div className="text-amber mt-2 space-y-1 text-[12px]">
              <p>
                {preview.conflictTransactions.length} row
                {preview.conflictTransactions.length === 1 ? "" : "s"} need
                review and will not be imported automatically.
              </p>
              {(() => {
                const driftCount = preview.conflictTransactions.filter(
                  (c) => c.reason === "date_drift"
                ).length;
                const descCount = preview.conflictTransactions.filter(
                  (c) => c.reason === "description_mismatch"
                ).length;
                return (
                  <ul className="text-fg-muted list-disc pl-4">
                    {driftCount > 0 ? (
                      <li>
                        {driftCount} possible duplicate
                        {driftCount === 1 ? "" : "s"} with different dates
                        (within ±7 days)
                      </li>
                    ) : null}
                    {descCount > 0 ? (
                      <li>
                        {descCount} same amount/date with a different
                        description
                      </li>
                    ) : null}
                  </ul>
                );
              })()}
            </div>
          ) : null}
          {preview.warnings?.length ? (
            <ul className="text-fg-muted mt-2 list-disc pl-4 text-[12px]">
              {preview.warnings.map((warning) => (
                <li key={warning}>{warning}</li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      {groups.length ? (
        <div className="border-border overflow-hidden rounded-[12px] border">
          <div className="border-border bg-surface-subtle text-fg-muted grid grid-cols-[0.7fr_1.4fr_0.9fr_0.8fr] gap-3 border-b px-4 py-2 text-[11px] font-medium tracking-[0.04em] uppercase">
            <span>Currency</span>
            <span>Account</span>
            <span>Range</span>
            <span className="text-right">Rows</span>
          </div>
          <div className="divide-border bg-surface divide-y">
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
        <div className="border-border text-danger rounded-[8px] border bg-[#F5DADA] px-3 py-2 text-[13px]">
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
            groups.every(
              (group) =>
                effectiveImportable(group, plans[importGroupKey(group)]) === 0
            ) ||
            groups.some(
              (group) => !isImportPlanReady(group, plans[importGroupKey(group)])
            )
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
        <span className="tabular text-fg font-medium">{group.currency}</span>
        {group.sourceKey ? (
          <p
            className="text-fg mt-1 truncate text-[12px] font-medium"
            title={group.sourceKey}
          >
            {group.sourceKey}
          </p>
        ) : null}
        <p className="text-fg-muted mt-1 text-[12px]">
          {group.dateFrom || "-"} to {group.dateTo || "-"}
        </p>
      </div>
      <div className="grid gap-2">
        <select
          className="border-border bg-surface h-9 rounded-[8px] border px-3"
          value={
            plan.action === "import_to_account"
              ? (plan.accountId ?? "")
              : "create"
          }
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
            className="border-border bg-surface h-9 rounded-[8px] border px-3"
            value={plan.kind}
            onChange={(e) => set({ kind: e.target.value as AccountKind })}
          >
            {ACCOUNT_KINDS.map((kind) => (
              <option key={kind.value} value={kind.value}>
                {kind.label}
              </option>
            ))}
          </select>
          <DateInput
            value={plan.openDate ?? ""}
            onChange={(iso) =>
              set({
                openDate: iso,
                openingBalanceDate: iso,
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
        <label className="text-fg-muted flex items-start gap-2 text-[12px]">
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
        <div className="text-fg-muted text-[12px]">Existing account</div>
      )}
      <div className="text-fg-muted text-right text-[12px]">
        <strong className="tabular text-fg">{counts.importable}</strong> new
        {counts.duplicates ? ` · ${counts.duplicates} dupes` : ""}
        {counts.conflicts ? ` · ${counts.conflicts} review` : ""}
      </div>
    </div>
  );
}

const decimalRe = /^-?\d+(\.\d+)?$/;

function importGroupKey(group: {
  currency: string;
  sourceKey?: string;
}): string {
  return `${group.currency}|${group.sourceKey ?? ""}`;
}

function effectiveImportable(
  group: ImportCurrencyGroup,
  plan?: ImportPlanGroup
) {
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
  groups,
  locale,
  workspaceId,
}: {
  accounts: Account[];
  groups: AccountGroup[];
  locale?: string;
  workspaceId: string;
}) {
  const queryClient = useQueryClient();
  const [newGroupName, setNewGroupName] = React.useState("");
  const [editingGroupId, setEditingGroupId] = React.useState<string | null>(
    null
  );
  const [draftGroupName, setDraftGroupName] = React.useState("");
  const [expandedBuckets, setExpandedBuckets] = React.useState<Set<string>>(
    () => new Set()
  );
  const [dragging, setDragging] = React.useState<
    { type: "group"; id: string } | { type: "account"; id: string } | null
  >(null);

  const sortedGroups = React.useMemo(
    () =>
      [...groups].sort(
        (a, b) =>
          a.sortOrder - b.sortOrder ||
          new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime()
      ),
    [groups]
  );
  const buckets = React.useMemo(
    () => buildAccountBuckets(accounts, sortedGroups),
    [accounts, sortedGroups]
  );

  const invalidate = React.useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["accounts", workspaceId] }),
      queryClient.invalidateQueries({
        queryKey: ["account-groups", workspaceId],
      }),
    ]);
  }, [queryClient, workspaceId]);

  const createGroupMutation = useMutation({
    mutationFn: (name: string) => createAccountGroup(workspaceId, { name }),
    onSuccess: async () => {
      setNewGroupName("");
      await invalidate();
    },
  });
  const updateGroupMutation = useMutation({
    mutationFn: ({
      id,
      patch,
    }: {
      id: string;
      patch: { name?: string; aggregateBalances?: boolean };
    }) => updateAccountGroup(workspaceId, id, patch),
    onSuccess: async () => {
      setEditingGroupId(null);
      await invalidate();
    },
  });
  const deleteGroupMutation = useMutation({
    mutationFn: (id: string) => deleteAccountGroup(workspaceId, id),
    onSuccess: invalidate,
  });
  const reorderMutation = useMutation({
    mutationFn: (next: ReturnType<typeof orderPayload>) =>
      reorderAccounts(workspaceId, next),
    onSuccess: invalidate,
  });

  const persistOrder = React.useCallback(
    (nextGroups: AccountGroup[], nextBuckets: AccountBucket[]) => {
      reorderMutation.mutate(orderPayload(nextGroups, nextBuckets));
    },
    [reorderMutation]
  );

  const moveGroup = (groupId: string, direction: -1 | 1) => {
    const index = sortedGroups.findIndex((group) => group.id === groupId);
    const target = index + direction;
    if (index < 0 || target < 0 || target >= sortedGroups.length) return;
    const nextGroups = [...sortedGroups];
    const currentGroup = nextGroups[index];
    const targetGroup = nextGroups[target];
    if (!currentGroup || !targetGroup) return;
    nextGroups[index] = targetGroup;
    nextGroups[target] = currentGroup;
    persistOrder(nextGroups, buckets);
  };

  const moveAccountToGroup = (accountId: string, groupId: string | null) => {
    const nextBuckets = cloneBuckets(buckets);
    const source = findAccount(nextBuckets, accountId);
    if (!source) return;
    const [account] = source.bucket.accounts.splice(source.index, 1);
    const target = nextBuckets.find((bucket) => bucket.groupId === groupId);
    if (!account || !target) return;
    target.accounts.push({ ...account, accountGroupId: groupId });
    persistOrder(sortedGroups, nextBuckets);
  };

  const moveAccount = (accountId: string, direction: -1 | 1) => {
    const nextBuckets = cloneBuckets(buckets);
    const source = findAccount(nextBuckets, accountId);
    if (!source) return;
    const target = source.index + direction;
    if (target < 0 || target >= source.bucket.accounts.length) return;
    const currentAccount = source.bucket.accounts[source.index];
    const targetAccount = source.bucket.accounts[target];
    if (!currentAccount || !targetAccount) return;
    source.bucket.accounts[source.index] = targetAccount;
    source.bucket.accounts[target] = currentAccount;
    persistOrder(sortedGroups, nextBuckets);
  };

  const toggleBucket = (key: string) => {
    setExpandedBuckets((current) => {
      const next = new Set(current);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  const dropBucket = (targetGroupId: string | null) => {
    if (!dragging) return;
    if (dragging.type === "group") {
      if (!targetGroupId) return;
      const from = sortedGroups.findIndex((group) => group.id === dragging.id);
      const to = sortedGroups.findIndex((group) => group.id === targetGroupId);
      if (from < 0 || to < 0 || from === to) return;
      const nextGroups = [...sortedGroups];
      const [moved] = nextGroups.splice(from, 1);
      if (!moved) return;
      nextGroups.splice(to, 0, moved);
      persistOrder(nextGroups, buckets);
      return;
    }
    moveAccountToGroup(dragging.id, targetGroupId);
  };

  const dropAccount = (targetAccount: Account) => {
    if (
      !dragging ||
      dragging.type !== "account" ||
      dragging.id === targetAccount.id
    ) {
      return;
    }
    const nextBuckets = cloneBuckets(buckets);
    const source = findAccount(nextBuckets, dragging.id);
    const target = findAccount(nextBuckets, targetAccount.id);
    if (!source || !target) return;
    const [account] = source.bucket.accounts.splice(source.index, 1);
    if (!account) return;
    const adjustedTargetIndex =
      source.bucket.groupId === target.bucket.groupId &&
      source.index < target.index
        ? target.index - 1
        : target.index;
    target.bucket.accounts.splice(adjustedTargetIndex, 0, {
      ...account,
      accountGroupId: target.bucket.groupId,
    });
    persistOrder(sortedGroups, nextBuckets);
  };

  const mutationError =
    (createGroupMutation.error instanceof ApiError &&
      createGroupMutation.error) ||
    (updateGroupMutation.error instanceof ApiError &&
      updateGroupMutation.error) ||
    (deleteGroupMutation.error instanceof ApiError &&
      deleteGroupMutation.error) ||
    (reorderMutation.error instanceof ApiError && reorderMutation.error) ||
    null;
  const busy =
    createGroupMutation.isPending ||
    updateGroupMutation.isPending ||
    deleteGroupMutation.isPending ||
    reorderMutation.isPending;

  return (
    <Card className="overflow-hidden">
      <div className="border-border bg-surface-subtle flex flex-col gap-3 border-b px-5 py-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <p className="text-fg text-[13px] font-medium">Account groups</p>
          <p className="text-fg-muted mt-1 text-[12px]">
            Drag accounts between groups, or use the move controls.
          </p>
        </div>
        <form
          className="flex min-w-0 gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            const name = newGroupName.trim();
            if (name) createGroupMutation.mutate(name);
          }}
        >
          <Input
            value={newGroupName}
            onChange={(event) => setNewGroupName(event.target.value)}
            placeholder="New group"
            className="h-9 w-[180px]"
          />
          <Button
            type="submit"
            size="sm"
            disabled={busy || !newGroupName.trim()}
          >
            <FolderPlus className="h-4 w-4" />
            Add
          </Button>
        </form>
      </div>

      {mutationError ? (
        <div className="border-border text-danger border-b bg-[#F5DADA] px-5 py-2 text-[12px]">
          {mutationError.body?.error || mutationError.message}
        </div>
      ) : null}

      <div className="divide-border divide-y">
        {buckets.map((bucket) => {
          const groupIndex = bucket.group
            ? sortedGroups.findIndex((group) => group.id === bucket.group?.id)
            : -1;
          const collapsed = !expandedBuckets.has(bucket.key);
          return (
            <section
              key={bucket.key}
              onDragOver={(event) => event.preventDefault()}
              onDrop={() => dropBucket(bucket.groupId)}
            >
              <div
                className="bg-surface flex items-center justify-between gap-3 px-5 py-3"
                draggable={!!bucket.group}
                onDragStart={() =>
                  bucket.group &&
                  setDragging({ type: "group", id: bucket.group.id })
                }
                onDragEnd={() => setDragging(null)}
              >
                <div className="flex min-w-0 items-center gap-2">
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 shrink-0"
                    onClick={(event) => {
                      event.stopPropagation();
                      toggleBucket(bucket.key);
                    }}
                    aria-expanded={!collapsed}
                    aria-label={
                      collapsed
                        ? `Expand ${bucket.name}`
                        : `Collapse ${bucket.name}`
                    }
                  >
                    {collapsed ? (
                      <ChevronRight className="h-4 w-4" />
                    ) : (
                      <ChevronDown className="h-4 w-4" />
                    )}
                  </Button>
                  {bucket.group ? (
                    <GripVertical className="text-fg-faint h-4 w-4 shrink-0" />
                  ) : null}
                  {editingGroupId === bucket.group?.id ? (
                    <div className="flex items-center gap-2">
                      <Input
                        value={draftGroupName}
                        onChange={(event) =>
                          setDraftGroupName(event.target.value)
                        }
                        className="h-8 max-w-[240px]"
                        autoFocus
                      />
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        disabled={busy}
                        onClick={() => {
                          const name = draftGroupName.trim();
                          if (bucket.group && name) {
                            updateGroupMutation.mutate({
                              id: bucket.group.id,
                              patch: { name },
                            });
                          }
                        }}
                        aria-label="Save group name"
                      >
                        <Check className="h-4 w-4" />
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        disabled={busy}
                        onClick={() => setEditingGroupId(null)}
                        aria-label="Cancel group edit"
                      >
                        <X className="h-4 w-4" />
                      </Button>
                    </div>
                  ) : (
                    <>
                      <h3 className="text-fg truncate text-[14px] font-medium">
                        {bucket.name}
                      </h3>
                      <Badge variant="neutral">{bucket.accounts.length}</Badge>
                      {bucket.group?.aggregateBalances ? (
                        <Badge variant="accent">One balance</Badge>
                      ) : null}
                    </>
                  )}
                </div>
                {bucket.group ? (
                  <div className="flex items-center gap-1">
                    <Button
                      type="button"
                      variant={
                        bucket.group.aggregateBalances ? "secondary" : "ghost"
                      }
                      size="sm"
                      disabled={busy}
                      onClick={() =>
                        bucket.group &&
                        updateGroupMutation.mutate({
                          id: bucket.group.id,
                          patch: {
                            aggregateBalances: !bucket.group.aggregateBalances,
                          },
                        })
                      }
                      aria-pressed={bucket.group.aggregateBalances}
                      aria-label={
                        bucket.group.aggregateBalances
                          ? "Count accounts individually"
                          : "Count group as one balance"
                      }
                      title={
                        bucket.group.aggregateBalances
                          ? "Count accounts individually"
                          : "Count group as one balance"
                      }
                    >
                      <Calculator className="h-4 w-4" />
                      <span className="hidden lg:inline">
                        {bucket.group.aggregateBalances ? "Grouped" : "Stats"}
                      </span>
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={busy || groupIndex <= 0}
                      onClick={() =>
                        bucket.group && moveGroup(bucket.group.id, -1)
                      }
                      aria-label="Move group up"
                    >
                      <ArrowUp className="h-4 w-4" />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={busy || groupIndex >= sortedGroups.length - 1}
                      onClick={() =>
                        bucket.group && moveGroup(bucket.group.id, 1)
                      }
                      aria-label="Move group down"
                    >
                      <ArrowDown className="h-4 w-4" />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={busy}
                      onClick={() => {
                        if (!bucket.group) return;
                        setEditingGroupId(bucket.group.id);
                        setDraftGroupName(bucket.group.name);
                      }}
                      aria-label="Rename group"
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={busy}
                      onClick={() =>
                        bucket.group &&
                        deleteGroupMutation.mutate(bucket.group.id)
                      }
                      aria-label="Delete group"
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                ) : null}
              </div>
              {!collapsed ? (
                <ul className="divide-border bg-surface divide-y">
                  {bucket.accounts.map((a, accountIndex) => (
                    <AccountRow
                      key={a.id}
                      account={a}
                      groups={sortedGroups}
                      locale={locale}
                      workspaceId={workspaceId}
                      dragDisabled={busy}
                      canMoveUp={accountIndex > 0}
                      canMoveDown={accountIndex < bucket.accounts.length - 1}
                      onMoveUp={() => moveAccount(a.id, -1)}
                      onMoveDown={() => moveAccount(a.id, 1)}
                      onMoveToGroup={(groupId) =>
                        moveAccountToGroup(a.id, groupId)
                      }
                      onDragStart={() =>
                        setDragging({ type: "account", id: a.id })
                      }
                      onDragEnd={() => setDragging(null)}
                      onDragOver={(event) => event.preventDefault()}
                      onDrop={(event) => {
                        event.stopPropagation();
                        dropAccount(a);
                      }}
                    />
                  ))}
                  {bucket.accounts.length === 0 ? (
                    <li className="text-fg-muted px-5 py-4 text-[12px]">
                      Drop accounts here to add them to this group.
                    </li>
                  ) : null}
                </ul>
              ) : null}
            </section>
          );
        })}
      </div>
    </Card>
  );
}

type AccountBucket = {
  key: string;
  groupId: string | null;
  name: string;
  group?: AccountGroup;
  accounts: Account[];
};

function buildAccountBuckets(
  accounts: Account[],
  groups: AccountGroup[]
): AccountBucket[] {
  const byGroup = new Map<string | null, Account[]>();
  byGroup.set(null, []);
  for (const group of groups) byGroup.set(group.id, []);
  for (const account of [...accounts].sort(accountOrder)) {
    const groupId = account.accountGroupId ?? null;
    const bucket = byGroup.get(groupId) ?? byGroup.get(null);
    bucket?.push(account);
  }
  return [
    ...groups.map((group) => ({
      key: group.id,
      groupId: group.id,
      name: group.name,
      group,
      accounts: byGroup.get(group.id) ?? [],
    })),
    {
      key: "ungrouped",
      groupId: null,
      name: "Ungrouped",
      accounts: byGroup.get(null) ?? [],
    },
  ];
}

function accountOrder(a: Account, b: Account) {
  return (
    a.accountSortOrder - b.accountSortOrder ||
    new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime()
  );
}

function cloneBuckets(buckets: AccountBucket[]): AccountBucket[] {
  return buckets.map((bucket) => ({
    ...bucket,
    accounts: [...bucket.accounts],
  }));
}

function findAccount(buckets: AccountBucket[], accountId: string) {
  for (const bucket of buckets) {
    const index = bucket.accounts.findIndex(
      (account) => account.id === accountId
    );
    if (index >= 0) return { bucket, index };
  }
  return null;
}

function orderPayload(groups: AccountGroup[], buckets: AccountBucket[]) {
  return {
    groups: groups.map((group, index) => ({
      id: group.id,
      sortOrder: (index + 1) * 1000,
    })),
    accounts: buckets.flatMap((bucket) =>
      bucket.accounts.map((account, index) => ({
        id: account.id,
        accountGroupId: bucket.groupId,
        sortOrder: (index + 1) * 1000,
      }))
    ),
  };
}

function AccountRow({
  account,
  groups,
  locale,
  workspaceId,
  dragDisabled,
  canMoveUp,
  canMoveDown,
  onMoveUp,
  onMoveDown,
  onMoveToGroup,
  onDragStart,
  onDragEnd,
  onDragOver,
  onDrop,
}: {
  account: Account;
  groups: AccountGroup[];
  locale?: string;
  workspaceId: string;
  dragDisabled: boolean;
  canMoveUp: boolean;
  canMoveDown: boolean;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onMoveToGroup: (groupId: string | null) => void;
  onDragStart: () => void;
  onDragEnd: () => void;
  onDragOver: (event: React.DragEvent<HTMLLIElement>) => void;
  onDrop: (event: React.DragEvent<HTMLLIElement>) => void;
}) {
  const queryClient = useQueryClient();
  const [mode, setMode] = React.useState<"view" | "edit" | "confirm-delete">(
    "view"
  );
  const [draftName, setDraftName] = React.useState(account.name);
  const [draftKind, setDraftKind] = React.useState<AccountKind>(account.kind);

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["accounts", workspaceId] });

  const editMutation = useMutation({
    mutationFn: (patch: { name?: string; kind?: AccountKind }) =>
      updateAccount(workspaceId, account.id, patch),
    onSuccess: async () => {
      await invalidate();
      setMode("view");
    },
  });

  const archiveMutation = useMutation({
    mutationFn: (archived: boolean) =>
      updateAccount(workspaceId, account.id, { archived }),
    onSuccess: invalidate,
  });

  const deleteMutation = useMutation({
    mutationFn: () => deleteAccount(workspaceId, account.id),
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
    <li
      className="hover:bg-surface-subtle flex flex-col gap-3 px-5 py-4 transition-colors sm:flex-row sm:items-start sm:justify-between"
      draggable={!dragDisabled && mode === "view"}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onDragOver={onDragOver}
      onDrop={onDrop}
    >
      <div className="flex min-w-0 gap-3">
        <GripVertical className="text-fg-faint mt-1 h-4 w-4 shrink-0" />
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
                className="border-border bg-surface h-8 rounded-[8px] border px-2 text-[13px]"
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
              <span className="text-fg text-[15px] font-medium">
                {account.name}
              </span>
              {account.nickname ? (
                <span className="text-fg-faint text-[12px]">
                  ({account.nickname})
                </span>
              ) : null}
              <Badge variant="neutral">{accountKindLabel(account.kind)}</Badge>
              {archived ? <Badge variant="amber">Archived</Badge> : null}
            </div>
          )}
          <div className="text-fg-muted text-[12px]">
            {account.currency}
            {account.institution ? `  -  ${account.institution}` : ""} - opened{" "}
            {formatDate(account.openDate, locale)}
          </div>
          <label className="text-fg-muted flex w-fit items-center gap-2 text-[12px]">
            Group
            <select
              className="border-border bg-surface h-8 rounded-[8px] border px-2 text-[12px]"
              value={account.accountGroupId ?? ""}
              disabled={dragDisabled}
              onChange={(event) => onMoveToGroup(event.target.value || null)}
            >
              {groups.map((group) => (
                <option key={group.id} value={group.id}>
                  {group.name}
                </option>
              ))}
              <option value="">Ungrouped</option>
            </select>
          </label>
          {apiError ? (
            <p className="text-danger text-[11px]">
              {apiError.body?.error || apiError.message}
            </p>
          ) : null}
        </div>
      </div>
      <div className="flex items-center gap-3">
        <div className="flex flex-col items-end">
          <span className="tabular text-fg text-[15px] font-medium">
            {formatAmount(account.balance, account.currency, locale)}
          </span>
          <span className="text-fg-faint text-[11px]">
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
          onMoveUp={onMoveUp}
          onMoveDown={onMoveDown}
          canMoveUp={canMoveUp}
          canMoveDown={canMoveDown}
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
  onMoveUp,
  onMoveDown,
  canMoveUp,
  canMoveDown,
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
  onMoveUp: () => void;
  onMoveDown: () => void;
  canMoveUp: boolean;
  canMoveDown: boolean;
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
        <p className="text-danger text-[11px]">
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
        onClick={onMoveUp}
        disabled={busy || !canMoveUp}
        aria-label="Move account up"
      >
        <ArrowUp className="h-4 w-4" />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onMoveDown}
        disabled={busy || !canMoveDown}
        aria-label="Move account down"
      >
        <ArrowDown className="h-4 w-4" />
      </Button>
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
