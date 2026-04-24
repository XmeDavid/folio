"use client";

import * as React from "react";
import { Menu, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { SideNav } from "./nav";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { useIdentity } from "@/lib/hooks/use-identity";
import { fetchMe } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import { clearIdentity } from "@/lib/tenant";

export function AppShell({ children }: { children: React.ReactNode }) {
  const identity = useIdentity();
  const [mobileOpen, setMobileOpen] = React.useState(false);

  const meQuery = useQuery({
    queryKey: ["me", identity.tenantId],
    queryFn: () =>
      identity.status === "authenticated"
        ? fetchMe(identity.tenantId)
        : Promise.reject(new Error("no tenant")),
    enabled: identity.status === "authenticated",
  });

  return (
    <div className="flex min-h-screen bg-[--color-page]">
      <aside className="sticky top-0 hidden h-screen w-[240px] shrink-0 border-r border-[--color-border] bg-[--color-surface] lg:block">
        <SideNav />
      </aside>

      {mobileOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/20 lg:hidden"
          onClick={() => setMobileOpen(false)}
        />
      )}
      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-50 w-[260px] border-r border-[--color-border] bg-[--color-surface] transition-transform duration-200 ease-out lg:hidden",
          mobileOpen ? "translate-x-0" : "-translate-x-full"
        )}
      >
        <SideNav onNavigate={() => setMobileOpen(false)} />
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-[--color-border] bg-[--color-page] px-4 sm:px-6">
          <Button
            variant="ghost"
            size="icon"
            className="lg:hidden"
            aria-label="Open navigation"
            onClick={() => setMobileOpen((v) => !v)}
          >
            {mobileOpen ? (
              <X className="h-4 w-4" />
            ) : (
              <Menu className="h-4 w-4" />
            )}
          </Button>

          <div className="flex min-w-0 flex-1 items-center gap-3">
            {meQuery.data ? (
              <div className="flex min-w-0 flex-col leading-tight">
                <span className="truncate text-[13px] font-medium text-[--color-fg]">
                  {meQuery.data.tenant.name}
                </span>
                <span className="truncate text-[11px] text-[--color-fg-faint]">
                  {meQuery.data.user.displayName} -{" "}
                  {meQuery.data.tenant.baseCurrency}
                </span>
              </div>
            ) : (
              <div className="text-[13px] text-[--color-fg-faint]">
                {identity.status === "authenticated" ? "Loading..." : "Folio"}
              </div>
            )}
          </div>

          <Badge variant="amber" title="Session auth is not implemented yet.">
            Dev tenant bridge
          </Badge>

          {identity.status === "authenticated" ? (
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                clearIdentity();
              }}
            >
              Switch tenant
            </Button>
          ) : null}
        </header>

        <main className="min-w-0 flex-1 px-4 py-6 sm:px-8 sm:py-10">
          {children}
        </main>
      </div>
    </div>
  );
}
