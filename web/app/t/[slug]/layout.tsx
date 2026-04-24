"use client";

import { use, useEffect } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useCurrentTenant, useIdentity } from "@/lib/hooks/use-identity";
import { TenantSwitcher } from "@/components/tenant-switcher";

export default function TenantLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const id = useIdentity();
  const tenant = useCurrentTenant(slug);
  const router = useRouter();

  useEffect(() => {
    if (id.status === "unauthenticated") {
      router.replace("/login" as Route);
      return;
    }
    if (id.status === "authenticated" && !tenant) {
      router.replace("/tenants" as Route);
    }
  }, [id.status, tenant, router]);

  if (id.status !== "authenticated" || !tenant) {
    return (
      <div className="p-6 text-sm text-muted-foreground">Loading…</div>
    );
  }

  return (
    <div className="flex min-h-dvh flex-col">
      <TopBar currentTenantSlug={slug} tenantName={tenant.name} />
      <main className="flex-1 p-6">{children}</main>
    </div>
  );
}

function TopBar({
  currentTenantSlug,
  tenantName,
}: {
  currentTenantSlug: string;
  tenantName: string;
}) {
  return (
    <header className="flex items-center justify-between border-b px-6 py-3">
      <div className="flex items-center gap-3">
        <span className="font-semibold">Folio</span>
        <span className="text-sm text-muted-foreground">/</span>
        <TenantSwitcher currentSlug={currentTenantSlug} />
      </div>
      <div className="text-sm text-muted-foreground">{tenantName}</div>
    </header>
  );
}
