"use client";

import { use, useEffect } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useCurrentTenant, useIdentity } from "@/lib/hooks/use-identity";
import { TenantShell } from "@/components/app/tenant-shell";

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
    return <div className="p-6 text-sm text-muted-foreground">Loading…</div>;
  }

  return <TenantShell tenant={tenant}>{children}</TenantShell>;
}
