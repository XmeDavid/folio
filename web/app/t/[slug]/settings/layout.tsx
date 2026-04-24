"use client";

import Link from "next/link";
import type { Route } from "next";
import { use } from "react";
import { TenantShell } from "@/components/app/tenant-shell";
import { useCurrentTenant } from "@/lib/hooks/use-identity";

export default function TenantSettingsLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const tenant = useCurrentTenant(slug);
  if (!tenant) return null;

  return (
    <TenantShell
      tenant={tenant}
      sidebar={
        <nav className="flex flex-col gap-1 text-sm">
          <SettingsLink href={`/t/${slug}/settings/tenant` as Route}>
            Tenant
          </SettingsLink>
          <SettingsLink href={`/t/${slug}/settings/members` as Route}>
            Members
          </SettingsLink>
          <SettingsLink href={`/t/${slug}/settings/invites` as Route}>
            Invites
          </SettingsLink>
        </nav>
      }
    >
      {children}
    </TenantShell>
  );
}

function SettingsLink({
  href,
  children,
}: {
  href: Route;
  children: React.ReactNode;
}) {
  return (
    <Link
      href={href}
      className="rounded px-2 py-1 hover:bg-accent hover:text-accent-foreground"
    >
      {children}
    </Link>
  );
}
