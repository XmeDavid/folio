"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight } from "lucide-react";
import { TenantGate } from "@/components/app/gate";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner } from "@/components/app/empty";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { fetchAccounts, fetchTransactions, fetchMe } from "@/lib/api/client";
import { useIdentity } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";
import { accountKindLabel } from "@/lib/accounts";

export default function HomePage() {
  return (
    <TenantGate>
      <DashboardInner />
    </TenantGate>
  );
}

function DashboardInner() {
  const identity = useIdentity();
  const tenantId =
    identity.status === "authenticated" ? identity.tenantId : null;

  const meQuery = useQuery({
    queryKey: ["me", tenantId],
    queryFn: () => fetchMe(tenantId!),
    enabled: !!tenantId,
  });
  const accountsQuery = useQuery({
    queryKey: ["accounts", tenantId],
    queryFn: () => fetchAccounts(tenantId!),
    enabled: !!tenantId,
  });
  const txQuery = useQuery({
    queryKey: ["transactions", tenantId, { limit: 8 }],
    queryFn: () => fetchTransactions(tenantId!, { limit: 8 }),
    enabled: !!tenantId,
  });

  const locale = meQuery.data?.tenant.locale;

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Overview"
        title={
          meQuery.data
            ? `Welcome back, ${meQuery.data.user.displayName}`
            : "Welcome to Folio"
        }
        description={
          meQuery.data
            ? `Base currency ${meQuery.data.tenant.baseCurrency}  -  cycle anchor ${meQuery.data.tenant.cycleAnchorDay}.`
            : "Loading your workspace..."
        }
      />

      {meQuery.isError ? (
        <ErrorBanner
          title="Couldn't reach the Folio API"
          description="Make sure the backend is running on :8080. The dev server proxies /api/* to it."
        />
      ) : null}

      <section className="grid gap-4 md:grid-cols-3">
        <StatCard
          label="Accounts"
          value={
            accountsQuery.isLoading
              ? "..."
              : String(accountsQuery.data?.length ?? 0)
          }
          hint="Active accounts"
        />
        <StatCard
          label="Transactions"
          value={txQuery.isLoading ? "..." : String(txQuery.data?.length ?? 0)}
          hint="Recent (showing up to 8)"
        />
        <StatCard
          label="Base currency"
          value={meQuery.data?.tenant.baseCurrency ?? "-"}
          hint="Reporting roll-up"
        />
      </section>

      <section className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between gap-3">
            <div className="flex flex-col gap-1">
              <CardTitle>Accounts</CardTitle>
              <p className="text-[12px] text-[--color-fg-muted]">
                Balances shown are derived on the server.
              </p>
            </div>
            <Button variant="secondary" size="sm" asChild>
              <Link href="/accounts">
                Manage
                <ArrowRight className="h-3.5 w-3.5" />
              </Link>
            </Button>
          </CardHeader>
          <CardContent>
            {accountsQuery.isLoading ? (
              <p className="text-[13px] text-[--color-fg-muted]">Loading...</p>
            ) : accountsQuery.data && accountsQuery.data.length > 0 ? (
              <ul className="flex flex-col divide-y divide-[--color-border]">
                {accountsQuery.data.slice(0, 6).map((a) => (
                  <li
                    key={a.id}
                    className="flex items-center justify-between gap-4 py-3 text-[14px]"
                  >
                    <div className="flex min-w-0 flex-col">
                      <span className="truncate font-medium text-[--color-fg]">
                        {a.name}
                      </span>
                      <span className="truncate text-[12px] text-[--color-fg-faint]">
                        {accountKindLabel(a.kind)}
                        {a.institution ? `  -  ${a.institution}` : ""}
                      </span>
                    </div>
                    <span className="tabular text-[14px] font-medium text-[--color-fg]">
                      {formatAmount(a.balance, a.currency, locale)}
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState
                title="No accounts yet"
                description="Add a checking account, a cash pot, or a liability to start the ledger."
                action={
                  <Button size="sm" asChild>
                    <Link href="/accounts">Add an account</Link>
                  </Button>
                }
              />
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between gap-3">
            <div className="flex flex-col gap-1">
              <CardTitle>Recent transactions</CardTitle>
              <p className="text-[12px] text-[--color-fg-muted]">
                Latest by booked date.
              </p>
            </div>
            <Button variant="secondary" size="sm" asChild>
              <Link href="/transactions">
                View all
                <ArrowRight className="h-3.5 w-3.5" />
              </Link>
            </Button>
          </CardHeader>
          <CardContent>
            {txQuery.isLoading ? (
              <p className="text-[13px] text-[--color-fg-muted]">Loading...</p>
            ) : txQuery.data && txQuery.data.length > 0 ? (
              <ul className="flex flex-col divide-y divide-[--color-border]">
                {txQuery.data.slice(0, 8).map((t) => (
                  <li
                    key={t.id}
                    className="flex items-center justify-between gap-4 py-3 text-[14px]"
                  >
                    <div className="flex min-w-0 flex-col">
                      <span className="truncate font-medium text-[--color-fg]">
                        {t.description ?? t.counterpartyRaw ?? "-"}
                      </span>
                      <span className="truncate text-[12px] text-[--color-fg-faint]">
                        {formatDate(t.bookedAt, locale)} -{" "}
                        <TxStatus status={t.status} />
                      </span>
                    </div>
                    <span className="tabular text-[14px] font-medium text-[--color-fg]">
                      {formatAmount(t.amount, t.currency, locale)}
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState
                title="No transactions yet"
                description="Record a manual entry or connect an import source."
                action={
                  <Button size="sm" asChild>
                    <Link href="/transactions">Record one</Link>
                  </Button>
                }
              />
            )}
          </CardContent>
        </Card>
      </section>

      <section>
        <Card>
          <CardHeader>
            <CardTitle>Planned next</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {PLANNED.map((p) => (
                <li
                  key={p.title}
                  className="flex flex-col gap-1 rounded-[8px] border border-[--color-border] bg-[--color-surface] px-3 py-3"
                >
                  <span className="text-[13px] font-medium text-[--color-fg]">
                    {p.title}
                  </span>
                  <span className="text-[12px] text-[--color-fg-muted]">
                    {p.body}
                  </span>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      </section>
    </div>
  );
}

function StatCard({
  label,
  value,
  hint,
}: {
  label: string;
  value: string;
  hint?: string;
}) {
  return (
    <Card>
      <CardContent className="flex flex-col gap-1 pt-5">
        <span className="text-[11px] font-medium tracking-[0.07em] text-[--color-fg-faint] uppercase">
          {label}
        </span>
        <span className="tabular text-[24px] font-normal tracking-tight text-[--color-fg]">
          {value}
        </span>
        {hint ? (
          <span className="text-[12px] text-[--color-fg-muted]">{hint}</span>
        ) : null}
      </CardContent>
    </Card>
  );
}

function TxStatus({ status }: { status: string }) {
  const variant =
    status === "reconciled"
      ? "success"
      : status === "voided"
        ? "danger"
        : status === "draft"
          ? "neutral"
          : "neutral";
  return (
    <Badge variant={variant as "success" | "danger" | "neutral"}>
      {status}
    </Badge>
  );
}

const PLANNED = [
  {
    title: "Connect a bank",
    body: "GoCardless start endpoint is wired; consent flow UI ships with imports.",
  },
  {
    title: "Budgets & cycles",
    body: "Cycle anchor is stored; planning UI arrives once transactions settle.",
  },
  {
    title: "Categories & merchants",
    body: "Per-transaction category + merchant enrichment is on the roadmap.",
  },
];
