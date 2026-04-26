"use client";

import * as React from "react";
import { use } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Plus,
  Search,
  X,
} from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { CreateTransactionForm } from "@/components/transactions/create-transaction-form";
import {
  fetchAccounts,
  fetchCategories,
  fetchMerchants,
  fetchTransactions,
  updateTransaction,
  type Account,
  type Category,
  type Merchant,
  type Transaction,
  type TransactionStatus,
} from "@/lib/api/client";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";

const PAGE_SIZE_OPTIONS = [25, 50, 100] as const;

type TransactionFilters = {
  search: string;
  accountId: string;
  categoryId: string;
  merchantId: string;
  status: "" | TransactionStatus;
  from: string;
  to: string;
  minAmount: string;
  maxAmount: string;
  uncategorized: boolean;
};

const EMPTY_FILTERS: TransactionFilters = {
  search: "",
  accountId: "",
  categoryId: "",
  merchantId: "",
  status: "",
  from: "",
  to: "",
  minAmount: "",
  maxAmount: "",
  uncategorized: false,
};

export default function TransactionsPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  const workspaceId = workspace?.id ?? null;
  const [creating, setCreating] = React.useState(false);
  const [selectedId, setSelectedId] = React.useState<string | null>(null);
  const [searchDraft, setSearchDraft] = React.useState("");
  const [filters, setFilters] =
    React.useState<TransactionFilters>(EMPTY_FILTERS);
  const [page, setPage] = React.useState(0);
  const [pageSize, setPageSize] =
    React.useState<(typeof PAGE_SIZE_OPTIONS)[number]>(50);

  const accountsQuery = useQuery({
    queryKey: ["accounts", workspaceId],
    queryFn: () => fetchAccounts(workspaceId!),
    enabled: !!workspaceId,
  });
  const categoriesQuery = useQuery({
    queryKey: ["categories", workspaceId],
    queryFn: () => fetchCategories(workspaceId!),
    enabled: !!workspaceId,
  });
  const merchantsQuery = useQuery({
    queryKey: ["merchants", workspaceId],
    queryFn: () => fetchMerchants(workspaceId!),
    enabled: !!workspaceId,
  });
  const txQuery = useQuery({
    queryKey: ["transactions", workspaceId, { filters, page, pageSize }],
    queryFn: () =>
      fetchTransactions(workspaceId!, {
        ...filters,
        status: filters.status || undefined,
        limit: pageSize + 1,
        offset: page * pageSize,
      }),
    enabled: !!workspaceId,
  });

  if (!workspace) return null;

  const locale = workspace.locale;
  const accounts = accountsQuery.data ?? [];
  const categories = categoriesQuery.data ?? [];
  const merchants = merchantsQuery.data ?? [];
  const fetchedTransactions = txQuery.data ?? [];
  const hasNextPage = fetchedTransactions.length > pageSize;
  const transactions = fetchedTransactions.slice(0, pageSize);
  const hasAccounts = accounts.length > 0;
  const hasActiveFilters = activeFilterCount(filters) > 0;
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

      {creating && workspaceId && hasAccounts ? (
        <Card>
          <CardHeader>
            <CardTitle>Manual transaction</CardTitle>
          </CardHeader>
          <CardContent>
            <CreateTransactionForm
              workspaceId={workspaceId}
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
      {merchantsQuery.isError ? (
        <ErrorBanner
          title="Couldn't load merchants"
          description="Merchant filters need the merchant list."
        />
      ) : null}

      {!hasAccounts && !accountsQuery.isLoading ? (
        <EmptyState
          title="Add an account first"
          description="Transactions must post to an account. Head to Accounts to create one."
        />
      ) : txQuery.isLoading || accountsQuery.isLoading ? (
        <LoadingText />
      ) : txQuery.data && (transactions.length > 0 || hasActiveFilters) ? (
        <TransactionTable
          transactions={transactions}
          accounts={accounts}
          categories={categories}
          merchants={merchants}
          locale={locale}
          workspaceId={workspaceId!}
          selectedId={selected?.id ?? null}
          appliedFilters={filters}
          searchDraft={searchDraft}
          page={page}
          pageSize={pageSize}
          hasNextPage={hasNextPage}
          onSelect={setSelectedId}
          onSearchDraftChange={setSearchDraft}
          onSearchSubmit={() => {
            setPage(0);
            setSelectedId(null);
            setFilters(normalizeFilters({ ...filters, search: searchDraft }));
          }}
          onFilterChange={(patch) => {
            setPage(0);
            setSelectedId(null);
            setFilters((current) => normalizeFilters({ ...current, ...patch }));
          }}
          onClearFilters={() => {
            setPage(0);
            setSelectedId(null);
            setSearchDraft("");
            setFilters(EMPTY_FILTERS);
          }}
          onQuickUncategorized={() => {
            const next = normalizeFilters({
              ...filters,
              uncategorized: !filters.uncategorized,
              categoryId: "",
            });
            setPage(0);
            setSelectedId(null);
            setFilters(next);
          }}
          onPageChange={(nextPage) => {
            setPage(nextPage);
            setSelectedId(null);
          }}
          onPageSizeChange={(nextPageSize) => {
            setPage(0);
            setSelectedId(null);
            setPageSize(nextPageSize);
          }}
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
  merchants,
  locale,
  workspaceId,
  selectedId,
  appliedFilters,
  searchDraft,
  page,
  pageSize,
  hasNextPage,
  onSelect,
  onSearchDraftChange,
  onSearchSubmit,
  onFilterChange,
  onClearFilters,
  onQuickUncategorized,
  onPageChange,
  onPageSizeChange,
}: {
  transactions: Transaction[];
  accounts: Account[];
  categories: Category[];
  merchants: Merchant[];
  locale?: string;
  workspaceId: string;
  selectedId: string | null;
  appliedFilters: TransactionFilters;
  searchDraft: string;
  page: number;
  pageSize: (typeof PAGE_SIZE_OPTIONS)[number];
  hasNextPage: boolean;
  onSelect: (id: string) => void;
  onSearchDraftChange: (value: string) => void;
  onSearchSubmit: () => void;
  onFilterChange: (filters: Partial<TransactionFilters>) => void;
  onClearFilters: () => void;
  onQuickUncategorized: () => void;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: (typeof PAGE_SIZE_OPTIONS)[number]) => void;
}) {
  const accountById = new Map(accounts.map((a) => [a.id, a]));
  const categoryById = new Map(categories.map((c) => [c.id, c]));
  const categoryOptions = categoryLeafOptions(categories);
  const hasFilters = activeFilterCount(appliedFilters) > 0;
  const selected =
    transactions.find((transaction) => transaction.id === selectedId) ??
    transactions[0];

  return (
    <section className="grid gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.65fr)]">
      <Card className="overflow-hidden">
        <TransactionFiltersPanel
          accounts={accounts}
          categories={categoryOptions}
          merchants={merchants}
          filters={appliedFilters}
          searchDraft={searchDraft}
          activeFilterCount={activeFilterCount(appliedFilters)}
          onSearchDraftChange={onSearchDraftChange}
          onSearchSubmit={onSearchSubmit}
          onFilterChange={onFilterChange}
          onClear={onClearFilters}
        />
        <div className="border-border flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3">
          <div className="text-fg-muted text-[13px]">
            {transactions.length > 0
              ? `${page * pageSize + 1}-${page * pageSize + transactions.length} shown`
              : "No matches"}
            {hasFilters ? " after filters" : ""}
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Button
              variant="secondary"
              size="sm"
              onClick={onQuickUncategorized}
            >
              <CheckCircle2 className="h-4 w-4" />
              {appliedFilters.uncategorized ? "Show all" : "Needs category"}
            </Button>
            <Select
              className="h-8 w-[92px] text-[12px]"
              value={String(pageSize)}
              onChange={(event) =>
                onPageSizeChange(
                  Number(
                    event.target.value
                  ) as (typeof PAGE_SIZE_OPTIONS)[number]
                )
              }
              aria-label="Rows per page"
            >
              {PAGE_SIZE_OPTIONS.map((option) => (
                <option key={option} value={option}>
                  {option} rows
                </option>
              ))}
            </Select>
            <PaginationControls
              page={page}
              hasNextPage={hasNextPage}
              onPageChange={onPageChange}
              compact
            />
          </div>
        </div>
        {transactions.length > 0 ? (
          <>
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
                        workspaceId={workspaceId}
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
          </>
        ) : (
          <EmptyState
            title="No matching transactions"
            description="Adjust the filters or clear them to return to the full ledger."
            action={
              <Button variant="secondary" onClick={onClearFilters}>
                <X className="h-4 w-4" />
                Clear filters
              </Button>
            }
            className="py-10"
          />
        )}
        <div className="border-border flex flex-wrap items-center justify-between gap-3 border-t px-5 py-3">
          <div className="text-fg-faint text-[12px]">
            Page {page + 1}
            {hasNextPage ? "" : " · end of results"}
          </div>
          <PaginationControls
            page={page}
            hasNextPage={hasNextPage}
            onPageChange={onPageChange}
          />
        </div>
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
          workspaceId={workspaceId}
          locale={locale}
        />
      ) : null}
    </section>
  );
}

function PaginationControls({
  page,
  hasNextPage,
  onPageChange,
  compact = false,
}: {
  page: number;
  hasNextPage: boolean;
  onPageChange: (page: number) => void;
  compact?: boolean;
}) {
  return (
    <div className="flex items-center gap-2">
      <Button
        variant="secondary"
        size={compact ? "icon" : "sm"}
        disabled={page === 0}
        onClick={() => onPageChange(Math.max(0, page - 1))}
      >
        <ChevronLeft className="h-4 w-4" />
        {compact ? <span className="sr-only">Previous page</span> : "Previous"}
      </Button>
      {compact ? (
        <span className="text-fg-faint tabular min-w-8 text-center text-[12px]">
          {page + 1}
        </span>
      ) : null}
      <Button
        variant="secondary"
        size={compact ? "icon" : "sm"}
        disabled={!hasNextPage}
        onClick={() => onPageChange(page + 1)}
      >
        {compact ? <span className="sr-only">Next page</span> : "Next"}
        <ChevronRight className="h-4 w-4" />
      </Button>
    </div>
  );
}

function TransactionFiltersPanel({
  accounts,
  categories,
  merchants,
  filters,
  searchDraft,
  activeFilterCount,
  onSearchDraftChange,
  onSearchSubmit,
  onFilterChange,
  onClear,
}: {
  accounts: Account[];
  categories: { id: string; label: string }[];
  merchants: Merchant[];
  filters: TransactionFilters;
  searchDraft: string;
  activeFilterCount: number;
  onSearchDraftChange: (value: string) => void;
  onSearchSubmit: () => void;
  onFilterChange: (filters: Partial<TransactionFilters>) => void;
  onClear: () => void;
}) {
  return (
    <form
      className="border-border grid gap-3 border-b px-5 py-4"
      onSubmit={(event) => {
        event.preventDefault();
        onSearchSubmit();
      }}
    >
      <div className="grid min-w-0 grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-[minmax(220px,1fr)_minmax(140px,160px)_minmax(140px,160px)_minmax(140px,160px)]">
        <label className="text-fg-muted relative flex flex-col gap-1.5 text-[12px] font-medium">
          Search
          <Search className="text-fg-faint pointer-events-none absolute bottom-2.5 left-3 h-4 w-4" />
          <Input
            className="pl-9"
            value={searchDraft}
            onChange={(event) => onSearchDraftChange(event.target.value)}
            placeholder="Merchant, description, notes"
          />
          <Button
            className="absolute right-1 bottom-1 h-7 w-7"
            size="icon"
            type="submit"
            aria-label="Search transactions"
          >
            <Search className="h-3.5 w-3.5" />
          </Button>
        </label>
        <label className="text-fg-muted flex flex-col gap-1.5 text-[12px] font-medium">
          Account
          <Select
            value={filters.accountId}
            onChange={(event) =>
              onFilterChange({ accountId: event.target.value })
            }
          >
            <option value="">All accounts</option>
            {accounts.map((account) => (
              <option key={account.id} value={account.id}>
                {account.name}
              </option>
            ))}
          </Select>
        </label>
        <label className="text-fg-muted flex flex-col gap-1.5 text-[12px] font-medium">
          Category
          <Select
            value={filters.categoryId}
            onChange={(event) =>
              onFilterChange({
                categoryId: event.target.value,
                uncategorized: event.target.value
                  ? false
                  : filters.uncategorized,
              })
            }
          >
            <option value="">All categories</option>
            {categories.map((category) => (
              <option key={category.id} value={category.id}>
                {category.label}
              </option>
            ))}
          </Select>
        </label>
        <label className="text-fg-muted flex flex-col gap-1.5 text-[12px] font-medium">
          Merchant
          <Select
            value={filters.merchantId}
            onChange={(event) =>
              onFilterChange({ merchantId: event.target.value })
            }
            disabled={merchants.length === 0}
          >
            <option value="">All merchants</option>
            {merchants.map((merchant) => (
              <option key={merchant.id} value={merchant.id}>
                {merchant.canonicalName}
              </option>
            ))}
          </Select>
        </label>
      </div>

      <div className="grid min-w-0 grid-cols-1 gap-3 md:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-[minmax(120px,140px)_minmax(120px,140px)_minmax(120px,140px)_minmax(120px,140px)_minmax(120px,140px)_minmax(180px,1fr)]">
        <label className="text-fg-muted flex flex-col gap-1.5 text-[12px] font-medium">
          From
          <Input
            type="date"
            value={filters.from}
            onChange={(event) => onFilterChange({ from: event.target.value })}
          />
        </label>
        <label className="text-fg-muted flex flex-col gap-1.5 text-[12px] font-medium">
          To
          <Input
            type="date"
            value={filters.to}
            onChange={(event) => onFilterChange({ to: event.target.value })}
          />
        </label>
        <label className="text-fg-muted flex flex-col gap-1.5 text-[12px] font-medium">
          Status
          <Select
            value={filters.status}
            onChange={(event) =>
              onFilterChange({
                status: event.target.value as TransactionFilters["status"],
              })
            }
          >
            <option value="">Any status</option>
            <option value="posted">Posted</option>
            <option value="reconciled">Reconciled</option>
            <option value="draft">Draft</option>
            <option value="voided">Voided</option>
          </Select>
        </label>
        <label className="text-fg-muted flex flex-col gap-1.5 text-[12px] font-medium">
          Min amount
          <Input
            inputMode="decimal"
            value={filters.minAmount}
            onChange={(event) =>
              onFilterChange({ minAmount: event.target.value })
            }
            placeholder="-100.00"
          />
        </label>
        <label className="text-fg-muted flex flex-col gap-1.5 text-[12px] font-medium">
          Max amount
          <Input
            inputMode="decimal"
            value={filters.maxAmount}
            onChange={(event) =>
              onFilterChange({ maxAmount: event.target.value })
            }
            placeholder="250.00"
          />
        </label>
        <div className="flex flex-wrap items-end gap-2">
          <label className="border-border text-fg-muted flex h-9 items-center gap-2 rounded-[8px] border px-3 text-[13px]">
            <input
              type="checkbox"
              className="h-3.5 w-3.5"
              checked={filters.uncategorized}
              onChange={(event) =>
                onFilterChange({
                  uncategorized: event.target.checked,
                  categoryId: event.target.checked ? "" : filters.categoryId,
                })
              }
            />
            Uncategorized
          </label>
          <Button type="button" variant="secondary" size="sm" onClick={onClear}>
            <X className="h-4 w-4" />
            Clear
            {activeFilterCount > 0 ? ` (${activeFilterCount})` : ""}
          </Button>
        </div>
      </div>
    </form>
  );
}

function CategorySelect({
  workspaceId,
  transaction,
  categories,
}: {
  workspaceId: string;
  transaction: Transaction;
  categories: { id: string; label: string }[];
}) {
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: (categoryId: string) =>
      updateTransaction(workspaceId, transaction.id, {
        categoryId: categoryId || null,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["transactions", workspaceId],
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
  workspaceId,
  locale,
}: {
  transaction: Transaction;
  account?: Account;
  category?: Category | null;
  categoryOptions: { id: string; label: string }[];
  workspaceId: string;
  locale?: string;
}) {
  const queryClient = useQueryClient();
  const [notes, setNotes] = React.useState(transaction.notes ?? "");

  const mutation = useMutation({
    mutationFn: (patch: {
      notes?: string | null;
      countAsExpense?: boolean | null;
    }) => updateTransaction(workspaceId, transaction.id, patch),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["transactions", workspaceId],
      });
      await queryClient.invalidateQueries({ queryKey: ["accounts", workspaceId] });
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
            workspaceId={workspaceId}
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

function normalizeFilters(filters: TransactionFilters): TransactionFilters {
  const next = {
    ...filters,
    search: filters.search.trim(),
    minAmount: filters.minAmount.trim(),
    maxAmount: filters.maxAmount.trim(),
  };
  if (next.uncategorized) {
    next.categoryId = "";
  }
  return next;
}

function activeFilterCount(filters: TransactionFilters): number {
  return [
    filters.search,
    filters.accountId,
    filters.categoryId,
    filters.merchantId,
    filters.status,
    filters.from,
    filters.to,
    filters.minAmount,
    filters.maxAmount,
    filters.uncategorized ? "uncategorized" : "",
  ].filter(Boolean).length;
}
