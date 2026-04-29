"use client";

import * as React from "react";
import { use } from "react";
import Link from "next/link";
import type { Route } from "next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  ArchiveRestore,
  ChevronLeft,
  GitMerge,
  Pencil,
  Plus,
  X,
} from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { FormError } from "@/components/ui/form-error";
import { Input } from "@/components/ui/input";
import { MerchantForm } from "@/components/classification/merchants-table";
import { MerchantMergeDialog } from "@/components/classification/merchant-merge-dialog";
import {
  ApiError,
  addMerchantAlias,
  archiveMerchant,
  fetchAccounts,
  fetchCategories,
  fetchMerchant,
  fetchTransactions,
  listMerchantAliases,
  removeMerchantAlias,
  updateMerchant,
  type Account,
  type Category,
  type Merchant,
  type MerchantAlias,
  type Transaction,
} from "@/lib/api/client";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";

export default function MerchantDetailPage({
  params,
}: {
  params: Promise<{ slug: string; merchantId: string }>;
}) {
  const { slug, merchantId } = use(params);
  const workspace = useCurrentWorkspace(slug);
  const workspaceId = workspace?.id ?? null;

  const merchantQuery = useQuery({
    queryKey: ["merchant", workspaceId, merchantId],
    queryFn: () => fetchMerchant(workspaceId!, merchantId),
    enabled: !!workspaceId,
    retry: (failureCount, error) => {
      if (error instanceof ApiError && error.status === 404) return false;
      return failureCount < 2;
    },
  });

  const categoriesQuery = useQuery({
    queryKey: ["categories", workspaceId, true],
    queryFn: () => fetchCategories(workspaceId!, { includeArchived: true }),
    enabled: !!workspaceId,
  });

  const accountsQuery = useQuery({
    queryKey: ["accounts", workspaceId, true],
    queryFn: () => fetchAccounts(workspaceId!, { includeArchived: true }),
    enabled: !!workspaceId,
  });

  const aliasesQuery = useQuery({
    queryKey: ["merchant-aliases", workspaceId, merchantId],
    queryFn: () => listMerchantAliases(workspaceId!, merchantId),
    enabled: !!workspaceId,
  });

  const transactionsQuery = useQuery({
    queryKey: ["transactions", workspaceId, { merchantId, limit: 200 }],
    queryFn: () =>
      fetchTransactions(workspaceId!, { merchantId, limit: 200 }),
    enabled: !!workspaceId,
  });

  if (!workspace) return null;

  const backHref = `/w/${slug}/merchants` as Route;

  if (merchantQuery.isLoading) {
    return (
      <div className="flex flex-col gap-8">
        <BackLink href={backHref} />
        <LoadingText />
      </div>
    );
  }

  if (
    merchantQuery.error instanceof ApiError &&
    merchantQuery.error.status === 404
  ) {
    return (
      <div className="flex flex-col gap-8">
        <BackLink href={backHref} />
        <ErrorBanner
          title="Merchant not found"
          description="It may have been deleted, or the link is incorrect."
          action={
            <Button asChild variant="secondary" size="sm">
              <Link href={backHref}>Back to merchants</Link>
            </Button>
          }
        />
      </div>
    );
  }

  if (merchantQuery.isError || !merchantQuery.data) {
    return (
      <div className="flex flex-col gap-8">
        <BackLink href={backHref} />
        <ErrorBanner
          title="Couldn't load merchant"
          description="Check that the backend is running and your session is still valid."
        />
      </div>
    );
  }

  const merchant = merchantQuery.data;
  const categories = categoriesQuery.data ?? [];
  const categoryById = new Map(categories.map((c) => [c.id, c]));
  const leafCategories = computeLeafCategories(categories);
  const accounts = accountsQuery.data ?? [];
  const accountById = new Map(accounts.map((a) => [a.id, a]));
  const aliases = aliasesQuery.data ?? [];
  const transactions = transactionsQuery.data ?? [];
  const locale = workspace.locale;

  return (
    <div className="flex flex-col gap-8">
      <BackLink href={backHref} />
      <PageHeader
        eyebrow="Classification › Merchant"
        title={merchant.canonicalName}
        description="All transactions and metadata for this merchant."
      />

      <div className="grid gap-6 lg:grid-cols-[320px_minmax(0,1fr)]">
        <MerchantDetailSidebar
          workspaceId={workspace.id}
          workspaceSlug={slug}
          merchant={merchant}
          categoryById={categoryById}
          leafCategories={leafCategories}
          transactionCount={transactions.length}
        />
        <MerchantTransactionsPanel
          transactions={transactions}
          isLoading={transactionsQuery.isLoading}
          isError={transactionsQuery.isError}
          accountById={accountById}
          categoryById={categoryById}
          locale={locale}
        />
      </div>

      <MerchantAliasesPanel
        workspaceId={workspace.id}
        merchantId={merchant.id}
        aliases={aliases}
        isLoading={aliasesQuery.isLoading}
        isError={aliasesQuery.isError}
      />
    </div>
  );
}

function BackLink({ href }: { href: Route }) {
  return (
    <Link
      href={href}
      className="inline-flex items-center gap-1 self-start text-[12px] text-fg-muted hover:text-fg"
    >
      <ChevronLeft className="h-3.5 w-3.5" />
      Back to merchants
    </Link>
  );
}

function MerchantDetailSidebar({
  workspaceId,
  workspaceSlug,
  merchant,
  categoryById,
  leafCategories,
  transactionCount,
}: {
  workspaceId: string;
  workspaceSlug: string;
  merchant: Merchant;
  categoryById: Map<string, Category>;
  leafCategories: Category[];
  transactionCount: number;
}) {
  const queryClient = useQueryClient();
  const [editing, setEditing] = React.useState(false);
  const [mergeOpen, setMergeOpen] = React.useState(false);

  const archiveMutation = useMutation({
    mutationFn: async () => {
      if (merchant.archivedAt) {
        await updateMerchant(workspaceId, merchant.id, { archived: false });
      } else {
        await archiveMerchant(workspaceId, merchant.id);
      }
    },
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: ["merchant", workspaceId, merchant.id],
        }),
        queryClient.invalidateQueries({
          queryKey: ["merchants", workspaceId],
        }),
      ]);
    },
  });

  const defaultCategory = merchant.defaultCategoryId
    ? categoryById.get(merchant.defaultCategoryId)
    : null;

  return (
    <Card className="flex flex-col gap-4 p-5">
      <div className="flex items-center gap-3">
        <MerchantAvatar logoUrl={merchant.logoUrl} />
        <div className="min-w-0">
          <div className="truncate text-[15px] font-medium text-fg">
            {merchant.canonicalName}
          </div>
          <div className="mt-1">
            {merchant.archivedAt ? (
              <Badge variant="neutral">Archived</Badge>
            ) : (
              <Badge variant="success">Active</Badge>
            )}
          </div>
        </div>
      </div>

      {editing ? (
        <div className="border-t border-border pt-4">
          <MerchantForm
            slug={workspaceSlug}
            workspaceId={workspaceId}
            leafCategories={leafCategories}
            merchant={merchant}
            transactionCount={transactionCount}
            onDone={() => setEditing(false)}
            onCancel={() => setEditing(false)}
          />
        </div>
      ) : (
        <dl className="flex flex-col gap-3 border-t border-border pt-4 text-[13px]">
          <SidebarField label="Default category">
            {defaultCategory ? (
              <span className="text-fg">{defaultCategory.name}</span>
            ) : (
              <span className="text-fg-faint">— none —</span>
            )}
          </SidebarField>
          <SidebarField label="Industry">
            {merchant.industry ? (
              <span className="text-fg">{merchant.industry}</span>
            ) : (
              <span className="text-fg-faint">—</span>
            )}
          </SidebarField>
          <SidebarField label="Website">
            {merchant.website ? (
              <a
                href={merchant.website}
                target="_blank"
                rel="noreferrer"
                className="text-fg hover:underline"
              >
                {merchant.website}
              </a>
            ) : (
              <span className="text-fg-faint">—</span>
            )}
          </SidebarField>
          <SidebarField label="Notes">
            {merchant.notes ? (
              <span className="whitespace-pre-line text-fg">
                {merchant.notes}
              </span>
            ) : (
              <span className="text-fg-faint">—</span>
            )}
          </SidebarField>
        </dl>
      )}

      {!editing ? (
        <div className="flex flex-col gap-2 border-t border-border pt-4">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setEditing(true)}
          >
            <Pencil className="h-3.5 w-3.5" />
            Edit details
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setMergeOpen(true)}
            disabled={!!merchant.archivedAt}
            title={
              merchant.archivedAt
                ? "Restore this merchant before merging."
                : undefined
            }
          >
            <GitMerge className="h-3.5 w-3.5" />
            Merge into…
          </Button>
          <Button
            variant="secondary"
            size="sm"
            disabled={archiveMutation.isPending}
            onClick={() => archiveMutation.mutate()}
          >
            {merchant.archivedAt ? (
              <>
                <ArchiveRestore className="h-3.5 w-3.5" />
                Restore
              </>
            ) : (
              <>
                <Archive className="h-3.5 w-3.5" />
                Archive
              </>
            )}
          </Button>
        </div>
      ) : null}
      <MerchantMergeDialog
        open={mergeOpen}
        workspaceId={workspaceId}
        workspaceSlug={workspaceSlug}
        source={merchant}
        onClose={() => setMergeOpen(false)}
      />
    </Card>
  );
}

function SidebarField({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1">
      <dt className="text-[11px] font-medium tracking-[0.07em] text-fg-faint uppercase">
        {label}
      </dt>
      <dd className="text-[13px] leading-snug">{children}</dd>
    </div>
  );
}

function MerchantAvatar({ logoUrl }: { logoUrl?: string | null }) {
  if (logoUrl) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={logoUrl}
        alt=""
        className="h-10 w-10 shrink-0 rounded border border-border bg-surface object-cover"
      />
    );
  }
  return (
    <div
      aria-hidden
      className="h-10 w-10 shrink-0 rounded border border-border bg-surface-subtle"
    />
  );
}

function MerchantTransactionsPanel({
  transactions,
  isLoading,
  isError,
  accountById,
  categoryById,
  locale,
}: {
  transactions: Transaction[];
  isLoading: boolean;
  isError: boolean;
  accountById: Map<string, Account>;
  categoryById: Map<string, Category>;
  locale?: string;
}) {
  if (isError) {
    return (
      <ErrorBanner
        title="Couldn't load transactions"
        description="Check that the backend is running and your session is still valid."
      />
    );
  }

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between border-b border-border px-5 py-3">
        <div className="text-[13px] font-medium text-fg">Transactions</div>
        <div className="text-[12px] text-fg-faint tabular-nums">
          {transactions.length > 0
            ? `${transactions.length} most recent`
            : null}
        </div>
      </div>
      {isLoading ? (
        <div className="px-5 py-6">
          <LoadingText />
        </div>
      ) : transactions.length === 0 ? (
        <EmptyState
          title="No transactions yet for this merchant."
          description="Once transactions reference this merchant, they'll appear here."
          className="m-5"
        />
      ) : (
        <>
          <div className="hidden grid-cols-[100px_minmax(0,1fr)_minmax(0,160px)_minmax(0,140px)_120px] items-center gap-4 border-b border-border px-5 py-2 text-[11px] font-medium tracking-[0.07em] uppercase text-fg-faint md:grid">
            <span>Date</span>
            <span>Description</span>
            <span>Category</span>
            <span>Account</span>
            <span className="text-right">Amount</span>
          </div>
          <ul className="divide-y divide-border">
            {transactions.map((t) => {
              const account = accountById.get(t.accountId);
              const category = t.categoryId
                ? categoryById.get(t.categoryId)
                : null;
              return (
                <li
                  key={t.id}
                  className="grid grid-cols-1 gap-2 px-5 py-3 text-[12px] md:grid-cols-[100px_minmax(0,1fr)_minmax(0,160px)_minmax(0,140px)_120px] md:items-center md:gap-4"
                >
                  <span className="tabular-nums text-fg-muted">
                    {formatDate(t.bookedAt, locale)}
                  </span>
                  <span className="truncate text-fg">
                    {t.counterpartyRaw ?? t.description ?? "—"}
                  </span>
                  <span className="truncate text-fg-muted">
                    {category ? (
                      category.name
                    ) : (
                      <span className="text-fg-faint">—</span>
                    )}
                  </span>
                  <span className="truncate text-fg-muted">
                    {account ? (
                      account.name
                    ) : (
                      <span className="text-fg-faint">—</span>
                    )}
                  </span>
                  <span
                    className={`tabular-nums text-right font-medium ${
                      t.amount.startsWith("-") ? "text-fg" : "text-success"
                    }`}
                  >
                    {formatAmount(t.amount, t.currency, locale)}
                  </span>
                </li>
              );
            })}
          </ul>
        </>
      )}
    </Card>
  );
}

function MerchantAliasesPanel({
  workspaceId,
  merchantId,
  aliases,
  isLoading,
  isError,
}: {
  workspaceId: string;
  merchantId: string;
  aliases: MerchantAlias[];
  isLoading: boolean;
  isError: boolean;
}) {
  const queryClient = useQueryClient();
  const [pattern, setPattern] = React.useState("");

  const invalidate = React.useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: ["merchant-aliases", workspaceId, merchantId],
      }),
      queryClient.invalidateQueries({
        queryKey: ["transactions", workspaceId],
      }),
    ]);
  }, [queryClient, workspaceId, merchantId]);

  const addMutation = useMutation({
    mutationFn: async (rawPattern: string) =>
      addMerchantAlias(workspaceId, merchantId, { rawPattern }),
    onSuccess: async () => {
      setPattern("");
      await invalidate();
    },
  });

  const removeMutation = useMutation({
    mutationFn: async (aliasId: string) =>
      removeMerchantAlias(workspaceId, merchantId, aliasId),
    onSuccess: async () => {
      await invalidate();
    },
  });

  const addError =
    addMutation.error instanceof ApiError ? addMutation.error.message : null;

  return (
    <Card className="overflow-hidden">
      <div className="flex items-center justify-between border-b border-border px-5 py-3">
        <div className="text-[13px] font-medium text-fg">
          Aliases
          {aliases.length > 0 ? (
            <span className="ml-2 text-fg-faint tabular-nums">
              ({aliases.length})
            </span>
          ) : null}
        </div>
        <p className="text-[12px] text-fg-faint">
          Raw counterparty strings that match this merchant during import.
        </p>
      </div>

      {isError ? (
        <div className="px-5 py-4">
          <ErrorBanner
            title="Couldn't load aliases"
            description="Check that the backend is running."
          />
        </div>
      ) : isLoading ? (
        <div className="px-5 py-4">
          <LoadingText />
        </div>
      ) : aliases.length === 0 ? (
        <div className="px-5 py-4 text-[13px] text-fg-muted">
          No aliases yet. Add a raw counterparty pattern below to capture
          incoming transactions automatically.
        </div>
      ) : (
        <ul className="divide-y divide-border">
          {aliases.map((alias) => (
            <li
              key={alias.id}
              className="flex items-center justify-between gap-3 px-5 py-2.5 text-[13px]"
            >
              <span className="truncate font-mono text-[12px] text-fg">
                {alias.rawPattern}
                {alias.isRegex ? (
                  <Badge variant="info" className="ml-2">
                    regex
                  </Badge>
                ) : null}
              </span>
              <Button
                variant="ghost"
                size="icon"
                disabled={removeMutation.isPending}
                onClick={() => removeMutation.mutate(alias.id)}
                aria-label="Remove alias"
              >
                <X className="h-4 w-4" />
                <span className="sr-only">Remove alias</span>
              </Button>
            </li>
          ))}
        </ul>
      )}

      <form
        className="flex flex-col gap-2 border-t border-border px-5 py-4"
        onSubmit={(event) => {
          event.preventDefault();
          const trimmed = pattern.trim();
          if (!trimmed) return;
          addMutation.mutate(trimmed);
        }}
      >
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            value={pattern}
            onChange={(event) => setPattern(event.target.value)}
            placeholder="e.g. COOP-4382 ZUR"
            aria-label="New alias pattern"
            className="sm:flex-1"
          />
          <Button
            type="submit"
            size="sm"
            disabled={addMutation.isPending || !pattern.trim()}
          >
            <Plus className="h-3.5 w-3.5" />
            Add alias
          </Button>
        </div>
        {addError ? <FormError>{addError}</FormError> : null}
      </form>
    </Card>
  );
}

function computeLeafCategories(categories: Category[]): Category[] {
  const parentIds = new Set<string>();
  for (const category of categories) {
    if (category.parentId) parentIds.add(category.parentId);
  }
  return categories
    .filter((category) => !parentIds.has(category.id))
    .filter((category) => !category.archivedAt)
    .sort((a, b) => a.name.localeCompare(b.name));
}
