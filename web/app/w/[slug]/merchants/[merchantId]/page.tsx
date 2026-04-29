"use client";

import { use } from "react";
import Link from "next/link";
import type { Route } from "next";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft } from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { MerchantAliasesPanel } from "@/components/classification/merchant-aliases";
import { MerchantDetailSidebar } from "@/components/classification/merchant-detail-sidebar";
import {
  ApiError,
  fetchAccounts,
  fetchCategories,
  fetchMerchant,
  fetchTransactions,
  listMerchantAliases,
  type Account,
  type Category,
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
