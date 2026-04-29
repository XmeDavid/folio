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
  applyAccountImportMulti,
  createAccountGroup,
  deleteAccount,
  deleteAccountGroup,
  fetchAccountGroups,
  fetchAccounts,
  previewAccountImportMulti,
  reorderAccounts,
  updateAccount,
  updateAccountGroup,
  type Account,
  type AccountGroup,
  type AccountKind,
  type ImportCurrencyGroup,
  type ImportPlanGroup,
  type ImportPreview,
  type MultiSmartImportEntry,
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
  // Files queued to hand to the import component on its next open. The page
  // owns this state so a drop anywhere on the Accounts route can open the
  // import card preloaded with the dropped files; SmartAccountImport drains
  // the queue on consumption.
  const [pendingFiles, setPendingFiles] = React.useState<File[]>([]);
  const [dragOverlay, setDragOverlay] = React.useState(false);

  // Page-level drag-and-drop. Listeners go on document so the user can drop
  // on any part of the Accounts page, not just a specific zone. We track an
  // event-counter via document.body.dataset so flicker between child
  // elements doesn't toggle the overlay off mid-drag (dragenter fires on
  // entering each child, dragleave on leaving each — naive boolean toggling
  // strobes).
  React.useEffect(() => {
    if (!workspaceId) return;
    let depth = 0;
    const isFileDrag = (event: DragEvent) =>
      Array.from(event.dataTransfer?.types ?? []).includes("Files");
    const onEnter = (event: DragEvent) => {
      if (!isFileDrag(event)) return;
      depth += 1;
      setDragOverlay(true);
    };
    const onOver = (event: DragEvent) => {
      if (!isFileDrag(event)) return;
      event.preventDefault();
    };
    const onLeave = (event: DragEvent) => {
      if (!isFileDrag(event)) return;
      depth -= 1;
      if (depth <= 0) {
        depth = 0;
        setDragOverlay(false);
      }
    };
    const onDrop = (event: DragEvent) => {
      if (!isFileDrag(event)) return;
      event.preventDefault();
      depth = 0;
      setDragOverlay(false);
      const dropped = Array.from(event.dataTransfer?.files ?? []);
      if (dropped.length === 0) return;
      setPendingFiles((current) => [...current, ...dropped]);
      setImporting(true);
      setCreating(false);
    };
    document.addEventListener("dragenter", onEnter);
    document.addEventListener("dragover", onOver);
    document.addEventListener("dragleave", onLeave);
    document.addEventListener("drop", onDrop);
    return () => {
      document.removeEventListener("dragenter", onEnter);
      document.removeEventListener("dragover", onOver);
      document.removeEventListener("dragleave", onLeave);
      document.removeEventListener("drop", onDrop);
    };
  }, [workspaceId]);

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
            <CardTitle>Import bank exports</CardTitle>
          </CardHeader>
          <CardContent>
            <SmartAccountImport
              workspaceId={workspaceId!}
              pendingFiles={pendingFiles}
              onConsumePendingFiles={() => setPendingFiles([])}
              onDone={() => {
                setImporting(false);
                setPendingFiles([]);
              }}
            />
          </CardContent>
        </Card>
      ) : null}

      {dragOverlay ? (
        <div
          className="bg-fg/40 pointer-events-none fixed inset-0 z-50 flex items-center justify-center backdrop-blur-sm"
          aria-hidden="true"
        >
          <div className="bg-surface text-fg border-border rounded-[16px] border-2 border-dashed px-8 py-6 text-center shadow-xl">
            <FileUp className="text-accent mx-auto mb-2 h-10 w-10" />
            <p className="text-[15px] font-semibold">Drop to import</p>
            <p className="text-fg-muted mt-1 text-[12px]">
              Multiple files supported — order doesn&apos;t matter.
            </p>
          </div>
        </div>
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

// FileBucket holds the post-preview state of one uploaded file: the
// preview/investment summary/error for that file and (for bank previews)
// the user's draft plan keyed per currency group. Bank-style files are the
// only ones that need apply-time confirmation; investment files are
// already ingested at preview time, so they only need a status echo.
type FileBucket = {
  id: string;
  fileName: string;
  status: "pending" | "bank" | "investment" | "error";
  preview?: ImportPreview;
  investment?: SmartInvestmentImportResult;
  error?: string;
  plans: Record<string, ImportPlanGroup>;
};

function makeBucket(file: File): FileBucket {
  return {
    id: `${file.name}|${file.size}|${file.lastModified}|${Math.random().toString(36).slice(2, 8)}`,
    fileName: file.name,
    status: "pending",
    plans: {},
  };
}

function bucketGroups(bucket: FileBucket): ImportCurrencyGroup[] {
  if (!bucket.preview) return [];
  if (bucket.preview.currencyGroups?.length) return bucket.preview.currencyGroups;
  const p = bucket.preview;
  return [
    {
      currency: p.suggestedCurrency ?? "",
      suggestedName: p.suggestedName ?? "Imported account",
      suggestedKind: p.suggestedKind ?? "checking",
      suggestedOpenDate: p.suggestedOpenDate,
      transactionCount: p.transactionCount,
      dateFrom: p.dateFrom,
      dateTo: p.dateTo,
      action: "create_account",
      importableCount: p.importableCount,
      duplicateCount: p.duplicateCount,
      conflictCount: p.conflictCount,
      sampleTransactions: p.sampleTransactions,
      conflictTransactions: p.conflictTransactions,
    },
  ];
}

function defaultPlansForPreview(
  preview: ImportPreview
): Record<string, ImportPlanGroup> {
  const next: Record<string, ImportPlanGroup> = {};
  const previewGroups = preview.currencyGroups?.length
    ? preview.currencyGroups
    : [
        {
          currency: preview.suggestedCurrency ?? "",
          suggestedName: preview.suggestedName ?? "Imported account",
          suggestedKind: preview.suggestedKind ?? "checking",
          suggestedOpenDate: preview.suggestedOpenDate,
          dateFrom: preview.dateFrom,
          existingAccountId: preview.existingAccountId,
          action: preview.existingAccountId
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
  return next;
}

function SmartAccountImport({
  workspaceId,
  pendingFiles,
  onConsumePendingFiles,
  onDone,
}: {
  workspaceId: string;
  pendingFiles: File[];
  onConsumePendingFiles: () => void;
  onDone: () => void;
}) {
  // One bucket per uploaded file. We keep them keyed by a per-file id so
  // re-uploads of the same name don't collide and so re-renders are stable.
  const [buckets, setBuckets] = React.useState<FileBucket[]>([]);

  const previewMutation = useMutation({
    mutationFn: async (files: File[]) => {
      const buckets = files.map(makeBucket);
      setBuckets((current) => [...current, ...buckets]);
      const response = await previewAccountImportMulti(workspaceId, files);
      return { buckets, response };
    },
    onSuccess: ({ buckets: newBuckets, response }) => {
      // Pair response entries to buckets by index — the backend preserves
      // upload order. Pairing by file name is fragile when the user drops
      // two files with the same name.
      setBuckets((current) => {
        const updated = [...current];
        newBuckets.forEach((bucket, i) => {
          const entry: MultiSmartImportEntry | undefined = response.files[i];
          const idx = updated.findIndex((b) => b.id === bucket.id);
          if (idx < 0) return;
          const base: FileBucket = updated[idx] ?? bucket;
          if (!entry) {
            updated[idx] = {
              ...base,
              status: "error",
              error: "no preview returned for this file",
            };
            return;
          }
          if (entry.kind === "investment") {
            updated[idx] = {
              ...base,
              fileName: entry.fileName || base.fileName,
              status: "investment",
              investment: entry.investment,
            };
          } else if (entry.kind === "error") {
            updated[idx] = {
              ...base,
              fileName: entry.fileName || base.fileName,
              status: "error",
              error: entry.error,
            };
          } else {
            updated[idx] = {
              ...base,
              fileName: entry.fileName || base.fileName,
              status: "bank",
              preview: entry.preview,
              plans: defaultPlansForPreview(entry.preview),
            };
          }
        });
        return updated;
      });
    },
    onError: (_err, files) => {
      // The whole-batch preview failed (network, auth, etc). Mark every
      // bucket we just queued so the UI can surface the error inline
      // without leaving them stuck in `pending`.
      const failedNames = files.map((f) => f.name);
      setBuckets((current) =>
        current.map((bucket) =>
          bucket.status === "pending" && failedNames.includes(bucket.fileName)
            ? {
                ...bucket,
                status: "error",
                error:
                  _err instanceof ApiError
                    ? _err.body?.error || _err.message
                    : "preview failed",
              }
            : bucket
        )
      );
    },
  });

  // Drain pendingFiles whenever it changes — page-level drop adds files
  // here, this effect ships them to preview.
  React.useEffect(() => {
    if (pendingFiles.length === 0) return;
    previewMutation.mutate(pendingFiles);
    onConsumePendingFiles();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pendingFiles]);

  const onSelectFiles = (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    if (files.length === 0) return;
    previewMutation.mutate(files);
  };

  const setBucketPlans = (
    bucketId: string,
    plans: Record<string, ImportPlanGroup>
  ) => {
    setBuckets((current) =>
      current.map((bucket) =>
        bucket.id === bucketId ? { ...bucket, plans } : bucket
      )
    );
  };

  const removeBucket = (bucketId: string) => {
    setBuckets((current) => current.filter((bucket) => bucket.id !== bucketId));
  };

  // Build the apply-multi payload from every bank bucket's plans. We skip
  // groups that have nothing importable (avoid noisy "0 rows" entries) and
  // backfill openingBalanceDate from openDate when the user didn't override.
  const buildApplyPayload = () => {
    const payload: { fileToken: string; groups: ImportPlanGroup[] }[] = [];
    for (const bucket of buckets) {
      if (bucket.status !== "bank" || !bucket.preview) continue;
      const groups = bucketGroups(bucket);
      const planGroups: ImportPlanGroup[] = [];
      for (const group of groups) {
        const plan = bucket.plans[importGroupKey(group)];
        if (!plan) continue;
        if (effectiveImportable(group, plan) === 0) continue;
        if (plan.action === "create_account") {
          planGroups.push({
            ...plan,
            openingBalanceDate: plan.openingBalanceDate || plan.openDate,
          });
        } else {
          planGroups.push(plan);
        }
      }
      if (planGroups.length === 0) continue;
      payload.push({ fileToken: bucket.preview.fileToken, groups: planGroups });
    }
    return payload;
  };

  const applyMutation = useMutation({
    mutationFn: async () => {
      const files = buildApplyPayload();
      if (files.length === 0) return null;
      return applyAccountImportMulti(workspaceId, files);
    },
    onSuccess: (result) => {
      // If the backend reported per-file errors, keep the panel open so the
      // user can see what went wrong. Otherwise close out.
      const hasErrors = result?.files.some((f) => f.error) ?? false;
      if (!hasErrors) {
        onDone();
      }
    },
  });

  const bankBuckets = buckets.filter((bucket) => bucket.status === "bank");
  const allReady =
    bankBuckets.length > 0 &&
    bankBuckets.every((bucket) => {
      const groups = bucketGroups(bucket);
      return groups.every((group) =>
        isImportPlanReady(group, bucket.plans[importGroupKey(group)])
      );
    });
  const anyImportable =
    bankBuckets.length > 0 &&
    bankBuckets.some((bucket) => {
      const groups = bucketGroups(bucket);
      return groups.some(
        (group) =>
          effectiveImportable(group, bucket.plans[importGroupKey(group)]) > 0
      );
    });

  const previewError =
    previewMutation.error instanceof ApiError ? previewMutation.error : null;
  const applyError =
    applyMutation.error instanceof ApiError ? applyMutation.error : null;
  const applyResult = applyMutation.data;

  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <div className="border-border bg-surface rounded-[12px] border px-4 py-3">
          <p className="text-[13px] font-medium">Smart import</p>
          <p className="text-fg-muted mt-1 text-[12px]">
            Drop one or more files anywhere on this page, or pick them here.
            Order doesn&apos;t matter — Folio matches existing accounts and
            cross-references duplicates across files automatically.
          </p>
        </div>
        <label className="flex flex-col gap-1.5 text-[13px] font-medium">
          Export files
          <Input
            type="file"
            multiple
            accept=".csv,.json,.xml,text/csv,application/json,application/xml"
            onChange={onSelectFiles}
          />
        </label>
      </div>

      {previewMutation.isPending ? (
        <p className="text-fg-muted text-[13px]">Reading {previewMutation.variables?.length ?? 0} file(s)...</p>
      ) : null}
      {previewError ? (
        <div className="border-border text-danger rounded-[8px] border bg-[#F5DADA] px-3 py-2 text-[13px]">
          {previewError.body?.error || previewError.message}
        </div>
      ) : null}

      {buckets.length === 0 ? (
        <div className="border-border bg-surface-subtle text-fg-muted rounded-[12px] border-2 border-dashed px-4 py-8 text-center text-[13px]">
          No files queued. Pick files above or drag them onto the page.
        </div>
      ) : null}

      <div className="flex flex-col gap-4">
        {buckets.map((bucket) => (
          <ImportFileSection
            key={bucket.id}
            bucket={bucket}
            onPlansChange={(plans) => setBucketPlans(bucket.id, plans)}
            onRemove={() => removeBucket(bucket.id)}
          />
        ))}
      </div>

      {applyError ? (
        <div className="border-border text-danger rounded-[8px] border bg-[#F5DADA] px-3 py-2 text-[13px]">
          {applyError.body?.error || applyError.message}
        </div>
      ) : null}

      {applyResult ? (
        <div className="border-border bg-surface rounded-[12px] border px-4 py-3 text-[12px]">
          <p className="text-[13px] font-medium">
            Imported{" "}
            <strong className="text-emerald-500">
              {applyResult.insertedCount}
            </strong>{" "}
            transaction(s) across {applyResult.files.length} file(s).
            {applyResult.duplicateCount
              ? ` ${applyResult.duplicateCount} duplicate(s) skipped.`
              : ""}
            {applyResult.conflictCount
              ? ` ${applyResult.conflictCount} need review.`
              : ""}
          </p>
          {applyResult.files.some((f) => f.error) ? (
            <ul className="text-danger mt-2 list-disc pl-4">
              {applyResult.files
                .filter((f) => f.error)
                .map((f, idx) => (
                  <li key={idx}>
                    {f.fileName ?? "(file)"}: {f.error}
                  </li>
                ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      <div className="flex justify-end gap-2">
        <Button type="button" variant="secondary" onClick={onDone}>
          Close
        </Button>
        <Button
          type="button"
          disabled={
            !anyImportable || !allReady || applyMutation.isPending
          }
          onClick={() => applyMutation.mutate()}
        >
          {applyMutation.isPending ? "Importing..." : "Apply import plan"}
        </Button>
      </div>
    </div>
  );
}

function ImportFileSection({
  bucket,
  onPlansChange,
  onRemove,
}: {
  bucket: FileBucket;
  onPlansChange: (plans: Record<string, ImportPlanGroup>) => void;
  onRemove: () => void;
}) {
  if (bucket.status === "pending") {
    return (
      <div className="border-border bg-surface rounded-[12px] border px-4 py-3 text-[13px]">
        <div className="flex items-center justify-between gap-3">
          <span className="text-fg font-medium">{bucket.fileName}</span>
          <span className="text-fg-muted text-[12px]">Reading...</span>
        </div>
      </div>
    );
  }
  if (bucket.status === "error") {
    return (
      <div className="border-border bg-[#F5DADA] text-danger rounded-[12px] border px-4 py-3 text-[13px]">
        <div className="flex items-center justify-between gap-3">
          <div>
            <p className="font-medium">{bucket.fileName}</p>
            <p className="text-[12px]">{bucket.error}</p>
          </div>
          <Button type="button" variant="ghost" size="sm" onClick={onRemove}>
            Remove
          </Button>
        </div>
      </div>
    );
  }
  if (bucket.status === "investment" && bucket.investment) {
    const inv = bucket.investment;
    return (
      <div className="border-border bg-surface rounded-[12px] border px-4 py-3">
        <div className="flex items-center justify-between gap-3">
          <p className="text-[13px] font-medium text-fg">
            {bucket.fileName} · investment activity imported into{" "}
            <span className="text-emerald-500">{inv.accountName}</span>
            {inv.created ? " (new account)" : ""}
          </p>
          <Button type="button" variant="ghost" size="sm" onClick={onRemove}>
            Dismiss
          </Button>
        </div>
        <p className="text-fg-muted mt-1 text-[12px]">
          Source: {inv.source.replace("_", " ")} · base {inv.baseCurrency}
        </p>
        <ul className="mt-2 grid gap-1 text-[12px] text-fg-muted sm:grid-cols-4">
          <li>
            Trades: <strong className="text-fg">{inv.summary.tradesCreated}</strong>
          </li>
          <li>
            Dividends:{" "}
            <strong className="text-fg">{inv.summary.dividendsCreated}</strong>
          </li>
          <li>
            Instruments:{" "}
            <strong className="text-fg">{inv.summary.instrumentsTouched}</strong>
          </li>
          <li>
            Skipped: <strong className="text-fg">{inv.summary.skipped}</strong>
          </li>
        </ul>
        {inv.summary.warnings && inv.summary.warnings.length > 0 ? (
          <details className="mt-2">
            <summary className="cursor-pointer text-[12px] text-fg-muted">
              {inv.summary.warnings.length} warning(s)
            </summary>
            <ul className="mt-1 list-disc pl-5 text-[12px] text-fg-muted">
              {inv.summary.warnings.map((w, i) => (
                <li key={i}>{w}</li>
              ))}
            </ul>
          </details>
        ) : null}
      </div>
    );
  }
  if (bucket.status !== "bank" || !bucket.preview) return null;

  const preview = bucket.preview;
  const groups = bucketGroups(bucket);

  return (
    <div className="border-border bg-surface flex flex-col gap-3 rounded-[12px] border px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-[13px] font-medium">
            {bucket.fileName} · {preview.dateFrom} to {preview.dateTo}
          </p>
          {!preview.currencyGroups?.length ? (
            <div className="text-fg-muted mt-1 grid gap-2 text-[12px] sm:grid-cols-4">
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
        </div>
        <Button type="button" variant="ghost" size="sm" onClick={onRemove}>
          Remove
        </Button>
      </div>

      {preview.warnings?.length ? (
        <ul className="text-fg-muted list-disc pl-4 text-[12px]">
          {preview.warnings.map((warning) => (
            <li key={warning}>{warning}</li>
          ))}
        </ul>
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
                  plan={bucket.plans[key]}
                  onPlanChange={(plan) =>
                    onPlansChange({ ...bucket.plans, [key]: plan })
                  }
                />
              );
            })}
          </div>
        </div>
      ) : null}
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
