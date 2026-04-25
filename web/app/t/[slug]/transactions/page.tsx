"use client";

import * as React from "react";
import { use } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { CreateTransactionForm } from "@/components/transactions/create-transaction-form";
import {
  fetchAccounts,
  fetchTransactions,
  type Account,
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

  const accountsQuery = useQuery({
    queryKey: ["accounts", tenantId],
    queryFn: () => fetchAccounts(tenantId!),
    enabled: !!tenantId,
  });
  const txQuery = useQuery({
    queryKey: ["transactions", tenantId, { limit: 100 }],
    queryFn: () => fetchTransactions(tenantId!, { limit: 100 }),
    enabled: !!tenantId,
  });

  if (!tenant) return null;

  const locale = tenant.locale;
  const accounts = accountsQuery.data ?? [];
  const hasAccounts = accounts.length > 0;

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

      {!hasAccounts && !accountsQuery.isLoading ? (
        <EmptyState
          title="Add an account first"
          description="Transactions must post to an account. Head to Accounts to create one."
        />
      ) : txQuery.isLoading || accountsQuery.isLoading ? (
        <p className="text-[13px] text-fg-muted">Loading...</p>
      ) : txQuery.data && txQuery.data.length > 0 ? (
        <TransactionTable
          transactions={txQuery.data}
          accounts={accounts}
          locale={locale}
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
  locale,
}: {
  transactions: Transaction[];
  accounts: Account[];
  locale?: string;
}) {
  const accountById = new Map(accounts.map((a) => [a.id, a]));

  return (
    <Card className="overflow-hidden">
      <div className="hidden grid-cols-[110px_1fr_160px_120px_140px] items-center gap-4 border-b border-border px-5 py-2 text-[11px] font-medium tracking-[0.07em] text-fg-faint uppercase md:grid">
        <span>Date</span>
        <span>Description</span>
        <span>Account</span>
        <span>Status</span>
        <span className="text-right">Amount</span>
      </div>
      <ul className="divide-y divide-border">
        {transactions.map((t) => {
          const account = accountById.get(t.accountId);
          return (
            <li
              key={t.id}
              className="grid grid-cols-1 gap-1 px-5 py-3 transition-colors hover:bg-surface-subtle md:grid-cols-[110px_1fr_160px_120px_140px] md:items-center md:gap-4"
            >
              <span className="tabular text-[13px] text-fg-muted">
                {formatDate(t.bookedAt, locale)}
              </span>
              <div className="flex min-w-0 flex-col">
                <span className="truncate text-[14px] font-medium text-fg">
                  {t.description ?? t.counterpartyRaw ?? "-"}
                </span>
                {t.counterpartyRaw && t.description ? (
                  <span className="truncate text-[12px] text-fg-faint">
                    {t.counterpartyRaw}
                  </span>
                ) : null}
              </div>
              <span className="truncate text-[13px] text-fg-muted">
                {account ? account.name : t.accountId.slice(0, 8)}
              </span>
              <span>
                <StatusBadge status={t.status} />
              </span>
              <span
                className={`tabular text-right text-[14px] font-medium ${
                  t.amount.startsWith("-")
                    ? "text-fg"
                    : "text-success"
                }`}
              >
                {formatAmount(t.amount, t.currency, locale)}
              </span>
            </li>
          );
        })}
      </ul>
    </Card>
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
