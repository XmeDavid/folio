"use client";

import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { TenantGate } from "@/components/app/gate";
import { PageHeader } from "@/components/app/page-header";
import { EmptyState, ErrorBanner } from "@/components/app/empty";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { CreateAccountForm } from "@/components/accounts/create-account-form";
import { fetchAccounts, type Account } from "@/lib/api/client";
import { useIdentity } from "@/lib/hooks/use-identity";
import { formatAmount, formatDate } from "@/lib/format";
import { accountKindLabel } from "@/lib/accounts";

export default function AccountsPage() {
  return (
    <TenantGate>
      <AccountsInner />
    </TenantGate>
  );
}

function AccountsInner() {
  const identity = useIdentity();
  const tenant =
    identity.status === "authenticated" ? identity.data.tenants[0] : null;
  const tenantId = tenant?.id ?? null;
  const [creating, setCreating] = React.useState(false);

  const accountsQuery = useQuery({
    queryKey: ["accounts", tenantId],
    queryFn: () => fetchAccounts(tenantId!),
    enabled: !!tenantId,
  });

  const locale = tenant?.locale;
  const baseCurrency = tenant?.baseCurrency ?? "CHF";

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        eyebrow="Ledger"
        title="Accounts"
        description="Every balance in Folio lives on an account. Start with checking or cash; credit cards and liabilities come next."
        actions={
          <Button onClick={() => setCreating((v) => !v)}>
            <Plus className="h-4 w-4" />
            {creating ? "Close" : "Add account"}
          </Button>
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

      {accountsQuery.isError ? (
        <ErrorBanner
          title="Couldn't load accounts"
          description="Is the backend running on :8080?"
        />
      ) : null}

      {accountsQuery.isLoading ? (
        <p className="text-[13px] text-[--color-fg-muted]">Loading...</p>
      ) : accountsQuery.data && accountsQuery.data.length > 0 ? (
        <AccountList accounts={accountsQuery.data} locale={locale} />
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

function AccountList({
  accounts,
  locale,
}: {
  accounts: Account[];
  locale?: string;
}) {
  return (
    <Card className="overflow-hidden">
      <ul className="divide-y divide-[--color-border]">
        {accounts.map((a) => (
          <li
            key={a.id}
            className="flex flex-col gap-3 px-5 py-4 transition-colors hover:bg-[--color-surface-subtle] sm:flex-row sm:items-center sm:justify-between"
          >
            <div className="flex min-w-0 flex-col gap-0.5">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-[15px] font-medium text-[--color-fg]">
                  {a.name}
                </span>
                {a.nickname ? (
                  <span className="text-[12px] text-[--color-fg-faint]">
                    ({a.nickname})
                  </span>
                ) : null}
                <Badge variant="neutral">{accountKindLabel(a.kind)}</Badge>
                {a.archivedAt ? <Badge variant="amber">Archived</Badge> : null}
              </div>
              <div className="text-[12px] text-[--color-fg-muted]">
                {a.currency}
                {a.institution ? `  -  ${a.institution}` : ""} - opened{" "}
                {formatDate(a.openDate, locale)}
              </div>
            </div>
            <div className="flex flex-col items-end">
              <span className="tabular text-[15px] font-medium text-[--color-fg]">
                {formatAmount(a.balance, a.currency, locale)}
              </span>
              <span className="text-[11px] text-[--color-fg-faint]">
                {a.balanceAsOf
                  ? `as of ${formatDate(a.balanceAsOf, locale)}`
                  : "no snapshot yet"}
              </span>
            </div>
          </li>
        ))}
      </ul>
    </Card>
  );
}
