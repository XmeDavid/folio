"use client";

import { use } from "react";
import Link from "next/link";
import type { Route } from "next";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Banknote, Plus, ReceiptText, Shapes } from "lucide-react";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner, LoadingText } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  fetchAccounts,
  fetchTransactions,
  type Account,
  type Transaction,
} from "@/lib/api/client";
import { useCurrentWorkspace } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";
import { addDecimalStrings } from "@/lib/decimal";
import { convertAmount, fetchLatestFxRates, type FxRate } from "@/lib/fx";

export default function WorkspaceDashboardPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const workspace = useCurrentWorkspace(slug);
  const workspaceId = workspace?.id ?? null;

  const accounts = useQuery({
    queryKey: ["accounts", workspaceId],
    queryFn: () => fetchAccounts(workspaceId!),
    enabled: !!workspaceId,
  });
  const transactions = useQuery({
    queryKey: ["transactions", workspaceId, { limit: 12 }],
    queryFn: () => fetchTransactions(workspaceId!, { limit: 12 }),
    enabled: !!workspaceId,
  });

  const locale = workspace?.locale;
  const accountRows = accounts.data ?? [];
  const transactionRows = transactions.data ?? [];
  const baseCurrency = workspace?.baseCurrency ?? "CHF";
  const networthAccounts = accountRows.filter(
    (account) => account.includeInNetworth
  );
  const balances = balanceByCurrency(networthAccounts);
  const networthCurrencies = [
    ...new Set(
      networthAccounts.map((account) => account.currency.toUpperCase())
    ),
  ].sort();
  const fxRates = useQuery({
    queryKey: ["fx-rates", baseCurrency, networthCurrencies],
    queryFn: () => fetchLatestFxRates(networthCurrencies, baseCurrency),
    enabled:
      !!workspaceId &&
      networthCurrencies.length > 0 &&
      networthCurrencies.some((currency) => currency !== baseCurrency),
    staleTime: 1000 * 60 * 60 * 6,
  });
  const networth = netWorthInBase(
    networthAccounts,
    baseCurrency,
    fxRates.data ?? {}
  );
  const uncategorized = transactionRows.filter((t) => !t.categoryId).length;
  if (!workspace) return null;
  const basePath = `/w/${workspace.slug}`;

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Workspace"
        title={workspace.name}
        description={`Base currency ${baseCurrency}. Current cycle anchors on day ${workspace.cycleAnchorDay}.`}
        actions={
          <div className="flex flex-wrap gap-2">
            <Button asChild variant="secondary">
              <Link href={`${basePath}/accounts` as Route}>
                <Plus className="h-4 w-4" />
                Add account
              </Link>
            </Button>
            <Button asChild>
              <Link href={`${basePath}/transactions` as Route}>
                <ReceiptText className="h-4 w-4" />
                Record transaction
              </Link>
            </Button>
          </div>
        }
      />

      {accounts.isError || transactions.isError ? (
        <ErrorBanner
          title="Couldn't load the dashboard"
          description="Check that the backend is running and your session is still valid."
        />
      ) : null}

      <section className="grid gap-4 md:grid-cols-4">
        <MetricCard
          icon={<Banknote className="h-4 w-4" />}
          label="Net worth"
          value={
            accounts.isLoading || fxRates.isLoading
              ? "..."
              : networthAccounts.length === 0
                ? "-"
                : formatAmount(networth.total, baseCurrency, locale)
          }
          detail={
            networth.missingCurrencies.length > 0
              ? `Excluding ${networth.missingCurrencies.join(", ")}`
              : "Converted to base currency"
          }
        />
        <MetricCard
          icon={<Banknote className="h-4 w-4" />}
          label="Accounts"
          value={accounts.isLoading ? "..." : String(accountRows.length)}
          detail={
            accountRows.length === 1
              ? "1 active balance"
              : `${accountRows.length} active balances`
          }
        />
        <MetricCard
          icon={<ReceiptText className="h-4 w-4" />}
          label="Recent transactions"
          value={
            transactions.isLoading ? "..." : String(transactionRows.length)
          }
          detail="Latest ledger activity"
        />
        <MetricCard
          icon={<Shapes className="h-4 w-4" />}
          label="Uncategorized"
          value={transactions.isLoading ? "..." : String(uncategorized)}
          detail="Needs classification"
          action={
            <Link
              href={`${basePath}/transactions` as Route}
              className="text-accent text-[12px] font-medium hover:underline"
            >
              Review
            </Link>
          }
        />
      </section>

      <section className="grid gap-4 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
        <Card>
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle>Net worth</CardTitle>
            <Button asChild variant="ghost" size="sm">
              <Link href={`${basePath}/accounts` as Route}>
                Accounts
                <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
          </CardHeader>
          <CardContent>
            {accounts.isLoading ? (
              <LoadingText />
            ) : balances.length > 0 ? (
              <div className="flex flex-col gap-5">
                <div>
                  <div className="text-fg-muted text-[12px] font-medium">
                    Workspace total
                  </div>
                  <div className="tabular mt-1 text-[30px] leading-tight font-normal">
                    {formatAmount(networth.total, baseCurrency, locale)}
                  </div>
                  <div className="text-fg-faint mt-1 text-[12px]">
                    {networth.missingCurrencies.length > 0
                      ? `Excludes currencies without rates: ${networth.missingCurrencies.join(", ")}`
                      : fxRates.data && Object.values(fxRates.data).length > 0
                        ? `Latest FX from ${rateProviders(fxRates.data)}, dated ${latestFxDate(fxRates.data)}`
                        : `All net-worth accounts are already in ${baseCurrency}`}
                  </div>
                </div>
                <div className="divide-border flex flex-col divide-y">
                  {balances.map(([currency, amount]) => (
                    <div
                      key={currency}
                      className="flex items-center justify-between py-3"
                    >
                      <span className="text-fg-muted text-[13px]">
                        {currency.toUpperCase()}
                      </span>
                      <span className="tabular text-[18px] font-medium">
                        {formatAmount(amount, currency, locale)}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            ) : (
              <EmptyState
                title={
                  accountRows.length > 0
                    ? "No net-worth accounts"
                    : "No accounts yet"
                }
                description={
                  accountRows.length > 0
                    ? "Turn on net-worth inclusion for at least one account."
                    : "Create the first account to start tracking balances."
                }
                action={
                  <Button asChild>
                    <Link href={`${basePath}/accounts` as Route}>
                      <Plus className="h-4 w-4" />
                      Add account
                    </Link>
                  </Button>
                }
                className="py-8"
              />
            )}
          </CardContent>
        </Card>

        <Card className="overflow-hidden">
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle>Recent transactions</CardTitle>
            <Button asChild variant="ghost" size="sm">
              <Link href={`${basePath}/transactions` as Route}>
                Ledger
                <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
          </CardHeader>
          {transactions.isLoading ? (
            <CardContent>
              <LoadingText />
            </CardContent>
          ) : transactionRows.length > 0 ? (
            <RecentTransactions
              transactions={transactionRows.slice(0, 8)}
              accounts={accountRows}
              locale={locale}
            />
          ) : (
            <CardContent>
              <EmptyState
                title="No transactions yet"
                description="Record a manual transaction once you have an account."
                action={
                  accountRows.length > 0 ? (
                    <Button asChild>
                      <Link href={`${basePath}/transactions` as Route}>
                        <Plus className="h-4 w-4" />
                        Record transaction
                      </Link>
                    </Button>
                  ) : null
                }
                className="py-8"
              />
            </CardContent>
          )}
        </Card>
      </section>
    </div>
  );
}

function MetricCard({
  icon,
  label,
  value,
  detail,
  action,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  detail: string;
  action?: React.ReactNode;
}) {
  return (
    <Card>
      <CardContent className="flex items-start justify-between gap-4 pt-5">
        <div className="flex min-w-0 flex-col gap-1">
          <div className="text-fg-muted flex items-center gap-2 text-[12px] font-medium">
            {icon}
            {label}
          </div>
          <div className="tabular text-[28px] leading-tight font-normal">
            {value}
          </div>
          <div className="text-fg-faint text-[12px]">{detail}</div>
        </div>
        {action ? <div className="shrink-0">{action}</div> : null}
      </CardContent>
    </Card>
  );
}

function RecentTransactions({
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
    <ul className="divide-border divide-y">
      {transactions.map((t) => {
        const account = accountById.get(t.accountId);
        return (
          <li
            key={t.id}
            className="hover:bg-surface-subtle grid grid-cols-[1fr_auto] gap-3 px-5 py-3 transition-colors"
          >
            <div className="min-w-0">
              <div className="truncate text-[14px] font-medium">
                {t.description ?? t.counterpartyRaw ?? "Untitled transaction"}
              </div>
              <div className="text-fg-muted flex flex-wrap items-center gap-2 text-[12px]">
                <span>{formatDate(t.bookedAt, locale)}</span>
                <span>·</span>
                <span className="truncate">
                  {account ? account.name : t.accountId.slice(0, 8)}
                </span>
                {!t.categoryId ? (
                  <Badge variant="amber">Uncategorized</Badge>
                ) : null}
              </div>
            </div>
            <div
              className={`tabular text-right text-[14px] font-medium ${
                t.amount.startsWith("-") ? "text-fg" : "text-success"
              }`}
            >
              {formatAmount(t.amount, t.currency, locale)}
            </div>
          </li>
        );
      })}
    </ul>
  );
}

function balanceByCurrency(accounts: Account[]): [string, string][] {
  const balances = new Map<string, string>();
  for (const account of accounts) {
    const currency = account.currency.toUpperCase();
    balances.set(
      currency,
      addDecimalStrings(balances.get(currency) ?? "0", account.balance)
    );
  }
  return [...balances.entries()].sort(([a], [b]) => a.localeCompare(b));
}

function netWorthInBase(
  accounts: Account[],
  baseCurrency: string,
  rates: Record<string, FxRate>
): { total: string; missingCurrencies: string[] } {
  let total = "0";
  const missing = new Set<string>();
  for (const account of accounts) {
    const converted = convertAmount(
      account.balance,
      account.currency,
      baseCurrency,
      rates
    );
    if (converted == null) {
      missing.add(account.currency.toUpperCase());
      continue;
    }
    total = addDecimalStrings(total, converted);
  }
  return { total, missingCurrencies: [...missing].sort() };
}

function latestFxDate(rates: Record<string, FxRate>): string {
  const dates = Object.values(rates)
    .map((rate) => rate.date)
    .sort();
  return dates[dates.length - 1] ?? "latest";
}

function rateProviders(rates: Record<string, FxRate>): string {
  const providers = [
    ...new Set(Object.values(rates).map((rate) => rate.provider)),
  ].sort();
  return providers
    .map((provider) =>
      provider === "frankfurter" ? "Frankfurter" : "Coinbase"
    )
    .join(" and ");
}
