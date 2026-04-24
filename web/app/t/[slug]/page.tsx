"use client";

import { use } from "react";
import { useQuery } from "@tanstack/react-query";
import { useCurrentTenant } from "@/lib/hooks/use-identity";
import { fetchAccounts, fetchTransactions } from "@/lib/api/client";

export default function TenantDashboardPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const tenant = useCurrentTenant(slug);

  const accounts = useQuery({
    queryKey: ["accounts", tenant?.id],
    queryFn: () => fetchAccounts(tenant!.id),
    enabled: !!tenant,
  });
  const transactions = useQuery({
    queryKey: ["transactions", tenant?.id, { limit: 8 }],
    queryFn: () => fetchTransactions(tenant!.id, { limit: 8 }),
    enabled: !!tenant,
  });

  if (!tenant) return null;

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold">{tenant.name}</h1>
      <p className="text-sm text-muted-foreground">
        Base currency {tenant.baseCurrency} · cycle anchor day {tenant.cycleAnchorDay}
      </p>
      <section className="grid gap-4 md:grid-cols-2">
        <Card title="Accounts">
          <pre className="overflow-auto text-xs">
            {accounts.isLoading ? "Loading..." : JSON.stringify(accounts.data, null, 2)}
          </pre>
        </Card>
        <Card title="Recent transactions">
          <pre className="overflow-auto text-xs">
            {transactions.isLoading ? "Loading..." : JSON.stringify(transactions.data, null, 2)}
          </pre>
        </Card>
      </section>
    </div>
  );
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="rounded border p-4">
      <h2 className="mb-2 text-sm font-semibold">{title}</h2>
      {children}
    </div>
  );
}
