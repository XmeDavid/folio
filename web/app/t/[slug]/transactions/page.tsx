"use client";

import * as React from "react";
import { use } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, ChevronRight, Plus } from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { CreateTransactionForm } from "@/components/transactions/create-transaction-form";
import {
  fetchAccounts,
  fetchCategories,
  fetchTransactions,
  updateTransaction,
  type Account,
  type Category,
  type Transaction,
  type TransactionStatus,
} from "@/lib/api/client";
import { useCurrentTenant } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";

export default function TransactionsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const tenant = useCurrentTenant(slug);
  const tenantId = tenant?.id ?? null;
  const [creating, setCreating] = React.useState(false);
  const [uncategorizedOnly, setUncategorizedOnly] = React.useState(false);
  const [selectedId, setSelectedId] = React.useState<string | null>(null);

  const accountsQuery = useQuery({
    queryKey: ["accounts", tenantId],
    queryFn: () => fetchAccounts(tenantId!),
    enabled: !!tenantId,
  });
  const categoriesQuery = useQuery({
    queryKey: ["categories", tenantId],
    queryFn: () => fetchCategories(tenantId!),
    enabled: !!tenantId,
  });
  const txQuery = useQuery({
    queryKey: ["transactions", tenantId, { limit: 100, uncategorizedOnly }],
    queryFn: () =>
      fetchTransactions(tenantId!, {
        limit: 100,
        uncategorized: uncategorizedOnly,
      }),
    enabled: !!tenantId,
  });

  if (!tenant) return null;

  const locale = tenant.locale;
  const accounts = accountsQuery.data ?? [];
  const categories = categoriesQuery.data ?? [];
  const transactions = txQuery.data ?? [];
  const hasAccounts = accounts.length > 0;
  const selected =
    transactions.find((transaction) => transaction.id === selectedId) ??
    transactions[0] ??
    null;

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Ledger"
        title="Transactions"
        description="Ordered by booked date. Manual entries post immediately; bank-sourced transactions will merge by auto-match once imports land."
        actions={
          <Button
            onClick={() => setCreating((v) => !v)}
            disabled={!hasAccounts}
          >
            <Plus className="h-4 w-4" />
            {creating ? "Close" : "Record transaction"}
          </Button>
        }
      />

      {creating && tenantId && hasAccounts ? (
        <Card>
          <CardHeader>
            <CardTitle>Manual transaction</CardTitle>
          </CardHeader>
          <CardContent>
            <CreateTransactionForm
              tenantId={tenantId}
              accounts={accounts}
              onCreated={() => setCreating(false)}
              onCancel={() => setCreating(false)}
            />
          </CardContent>
        </Card>
      ) : null}

      {txQuery.isError ? (
        <ErrorBanner
          title="Couldn't load transactions"
          description="Is the backend running on :8080?"
        />
      ) : null}
      {categoriesQuery.isError ? (
        <ErrorBanner
          title="Couldn't load categories"
          description="Categorization controls need the category list."
        />
      ) : null}

      {!hasAccounts && !accountsQuery.isLoading ? (
        <EmptyState
          title="Add an account first"
          description="Transactions must post to an account. Head to Accounts to create one."
        />
      ) : txQuery.isLoading || accountsQuery.isLoading ? (
        <LoadingText />
      ) : txQuery.data && txQuery.data.length > 0 ? (
        <TransactionTable
          transactions={txQuery.data}
          accounts={accounts}
          categories={categories}
          locale={locale}
          tenantId={tenantId!}
          selectedId={selected?.id ?? null}
          uncategorizedOnly={uncategorizedOnly}
          onSelect={setSelectedId}
          onToggleUncategorized={() => setUncategorizedOnly((v) => !v)}
        />
      ) : (
        <EmptyState
          title="No transactions yet"
          description="Every posted, planned, or scheduled financial event is a transaction."
          action={
            hasAccounts ? (
              <Button onClick={() => setCreating(true)}>
                <Plus className="h-4 w-4" />
                Record transaction
              </Button>
            ) : null
          }
        />
      )}
    </div>
  );
}

function TransactionTable({
  transactions,
  accounts,
  categories,
  locale,
  tenantId,
  selectedId,
  uncategorizedOnly,
  onSelect,
  onToggleUncategorized,
}: {
  transactions: Transaction[];
  accounts: Account[];
  categories: Category[];
  locale?: string;
  tenantId: string;
  selectedId: string | null;
  uncategorizedOnly: boolean;
  onSelect: (id: string) => void;
  onToggleUncategorized: () => void;
}) {
  const accountById = new Map(accounts.map((a) => [a.id, a]));
  const categoryById = new Map(categories.map((c) => [c.id, c]));
  const categoryOptions = categoryLeafOptions(categories);
  const selected =
    transactions.find((transaction) => transaction.id === selectedId) ??
    transactions[0];

  return (
    <section className="grid gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.65fr)]">
      <Card className="overflow-hidden">
        <div className="border-border flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3">
          <div className="text-fg-muted text-[13px]">
            {transactions.length} shown
            {uncategorizedOnly ? " needing categorization" : ""}
          </div>
          <Button variant="secondary" size="sm" onClick={onToggleUncategorized}>
            <CheckCircle2 className="h-4 w-4" />
            {uncategorizedOnly ? "Show all" : "Needs category"}
          </Button>
        </div>
        <div className="border-border text-fg-faint hidden grid-cols-[110px_1fr_150px_190px_105px_130px] items-center gap-4 border-b px-5 py-2 text-[11px] font-medium tracking-[0.07em] uppercase lg:grid">
          <span>Date</span>
          <span>Description</span>
          <span>Account</span>
          <span>Category</span>
          <span>Status</span>
          <span className="text-right">Amount</span>
        </div>
        <ul className="divide-border divide-y">
          {transactions.map((t) => {
            const account = accountById.get(t.accountId);
            const active = t.id === selected?.id;
            return (
              <li
                key={t.id}
                role="button"
                tabIndex={0}
                onClick={() => onSelect(t.id)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    onSelect(t.id);
                  }
                }}
                className={`hover:bg-surface-subtle grid cursor-pointer grid-cols-1 gap-2 px-5 py-3 transition-colors lg:grid-cols-[110px_1fr_150px_190px_105px_130px] lg:items-center lg:gap-4 ${
                  active ? "bg-surface-subtle" : ""
                }`}
              >
                <span className="tabular text-fg-muted text-[13px]">
                  {formatDate(t.bookedAt, locale)}
                </span>
                <div className="flex min-w-0 items-start justify-between gap-3 lg:block">
                  <div className="flex min-w-0 flex-col">
                    <span className="text-fg truncate text-[14px] font-medium">
                      {t.description ?? t.counterpartyRaw ?? "-"}
                    </span>
                    {t.counterpartyRaw && t.description ? (
                      <span className="text-fg-faint truncate text-[12px]">
                        {t.counterpartyRaw}
                      </span>
                    ) : null}
                  </div>
                  <ChevronRight className="text-fg-faint mt-0.5 h-4 w-4 shrink-0 lg:hidden" />
                </div>
                <span className="text-fg-muted truncate text-[13px]">
                  {account ? account.name : t.accountId.slice(0, 8)}
                </span>
                <div onClick={(event) => event.stopPropagation()}>
                  <CategorySelect
                    tenantId={tenantId}
                    transaction={t}
                    categories={categoryOptions}
                  />
                </div>
                <span>
                  <StatusBadge status={t.status} />
                </span>
                <span
                  className={`tabular text-right text-[14px] font-medium ${
                    t.amount.startsWith("-") ? "text-fg" : "text-success"
                  }`}
                >
                  {formatAmount(t.amount, t.currency, locale)}
                </span>
              </li>
            );
          })}
        </ul>
      </Card>

      {selected ? (
        <TransactionDetail
          key={selected.id}
          transaction={selected}
          account={accountById.get(selected.accountId)}
          category={
            selected.categoryId ? categoryById.get(selected.categoryId) : null
          }
          categoryOptions={categoryOptions}
          tenantId={tenantId}
          locale={locale}
        />
      ) : null}
    </section>
  );
}

function CategorySelect({
  tenantId,
  transaction,
  categories,
}: {
  tenantId: string;
  transaction: Transaction;
  categories: { id: string; label: string }[];
}) {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: (categoryId: string) =>
      updateTransaction(tenantId, transaction.id, {
        categoryId: categoryId || null,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["transactions", tenantId],
      });
    },
  });

  return (
    <Select
      value={transaction.categoryId ?? ""}
      onChange={(event) => mutation.mutate(event.target.value)}
      disabled={mutation.isPending || categories.length === 0}
      className="h-8 text-[13px]"
      aria-label="Transaction category"
    >
      <option value="">Uncategorized</option>
      {categories.map((category) => (
        <option key={category.id} value={category.id}>
          {category.label}
        </option>
      ))}
    </Select>
  );
}

function TransactionDetail({
  transaction,
  account,
  category,
  categoryOptions,
  tenantId,
  locale,
}: {
  transaction: Transaction;
  account?: Account;
  category?: Category | null;
  categoryOptions: { id: string; label: string }[];
  tenantId: string;
  locale?: string;
}) {
  const queryClient = useQueryClient();
  const [notes, setNotes] = React.useState(transaction.notes ?? "");

  const mutation = useMutation({
    mutationFn: (patch: {
      notes?: string | null;
      countAsExpense?: boolean | null;
    }) => updateTransaction(tenantId, transaction.id, patch),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["transactions", tenantId],
      });
      await queryClient.invalidateQueries({ queryKey: ["accounts", tenantId] });
    },
  });

  return (
    <Card className="h-fit">
      <CardHeader>
        <CardTitle>Transaction details</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <div className="flex flex-col gap-1">
          <div className="text-fg text-[16px] font-medium">
            {transaction.description ??
              transaction.counterpartyRaw ??
              "Untitled transaction"}
          </div>
          <div className="text-fg-muted text-[12px]">
            {formatDate(transaction.bookedAt, locale)} ·{" "}
            {account?.name ?? transaction.accountId.slice(0, 8)}
          </div>
        </div>

        <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-[13px]">
          <DetailTerm
            label="Amount"
            value={formatAmount(
              transaction.amount,
              transaction.currency,
              locale
            )}
          />
          <DetailTerm
            label="Status"
            value={<StatusBadge status={transaction.status} />}
          />
          <DetailTerm
            label="Category"
            value={category?.name ?? "Uncategorized"}
          />
          <DetailTerm
            label="Raw counterparty"
            value={transaction.counterpartyRaw ?? "-"}
          />
          {transaction.originalAmount && transaction.originalCurrency ? (
            <DetailTerm
              label="Original amount"
              value={formatAmount(
                transaction.originalAmount,
                transaction.originalCurrency,
                locale
              )}
            />
          ) : null}
        </dl>

        <div className="flex flex-col gap-1.5">
          <span className="text-fg-muted text-[12px] font-medium">
            Category
          </span>
          <CategorySelect
            tenantId={tenantId}
            transaction={transaction}
            categories={categoryOptions}
          />
        </div>

        <label className="text-fg-muted flex items-center gap-2 text-[13px]">
          <input
            type="checkbox"
            className="h-3.5 w-3.5"
            checked={transaction.countAsExpense ?? false}
            onChange={(event) =>
              mutation.mutate({ countAsExpense: event.target.checked })
            }
          />
          Count in expense reporting
        </label>

        <div className="flex flex-col gap-2">
          <span className="text-fg-muted text-[12px] font-medium">Notes</span>
          <Textarea
            value={notes}
            onChange={(event) => setNotes(event.target.value)}
            placeholder="Add context, receipt notes, or reimbursement details."
          />
          <div className="flex justify-end">
            <Button
              size="sm"
              variant="secondary"
              disabled={
                mutation.isPending || notes === (transaction.notes ?? "")
              }
              onClick={() => mutation.mutate({ notes: notes.trim() || null })}
            >
              Save notes
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function DetailTerm({
  label,
  value,
}: {
  label: string;
  value: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-fg-faint text-[11px] font-medium tracking-[0.07em] uppercase">
        {label}
      </dt>
      <dd className="text-fg mt-1 truncate">{value}</dd>
    </div>
  );
}

function StatusBadge({ status }: { status: TransactionStatus }) {
  switch (status) {
    case "reconciled":
      return <Badge variant="success">Reconciled</Badge>;
    case "voided":
      return <Badge variant="danger">Voided</Badge>;
    case "draft":
      return <Badge variant="amber">Draft</Badge>;
    case "posted":
    default:
      return <Badge variant="neutral">Posted</Badge>;
  }
}

function categoryLeafOptions(
  categories: Category[]
): { id: string; label: string }[] {
  const children = new Set(
    categories
      .map((category) => category.parentId)
      .filter((id): id is string => Boolean(id))
  );
  const byId = new Map(categories.map((category) => [category.id, category]));

  return categories
    .filter((category) => !children.has(category.id))
    .map((category) => ({
      id: category.id,
      label: categoryPath(category, byId),
    }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

function categoryPath(category: Category, byId: Map<string, Category>): string {
  const parts = [category.name];
  let parentId = category.parentId ?? null;
  while (parentId) {
    const parent = byId.get(parentId);
    if (!parent) break;
    parts.unshift(parent.name);
    parentId = parent.parentId ?? null;
  }
  return parts.join(" / ");
}
