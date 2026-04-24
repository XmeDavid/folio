"use client";

import type { ReactNode } from "react";
import { TenantSwitcher } from "@/components/tenant-switcher";
import type { MeTenant } from "@/lib/hooks/use-identity";

export function TenantShell({
  tenant,
  children,
  sidebar,
}: {
  tenant: MeTenant;
  children: ReactNode;
  sidebar?: ReactNode;
}) {
  return (
    <div className="flex min-h-dvh flex-col">
      <header className="flex items-center justify-between border-b px-6 py-3">
        <div className="flex items-center gap-3">
          <span className="font-semibold">Folio</span>
          <span className="text-sm text-muted-foreground">/</span>
          <TenantSwitcher currentSlug={tenant.slug} />
        </div>
        <div className="text-sm text-muted-foreground">{tenant.name}</div>
      </header>
      <div className="flex flex-1">
        {sidebar ? (
          <aside className="w-56 border-r p-4">{sidebar}</aside>
        ) : null}
        <main className="flex-1 p-6">{children}</main>
      </div>
    </div>
  );
}
