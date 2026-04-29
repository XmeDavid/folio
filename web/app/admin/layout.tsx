"use client";

import Link from "next/link";
import type { Route } from "next";
import { usePathname } from "next/navigation";
import { ArrowLeft } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { useAdminGuard } from "@/lib/hooks/use-admin-guard";
import { useIdentity } from "@/lib/hooks/use-identity";
import { cn } from "@/lib/utils";

const nav = [
  { href: "/admin/workspaces", label: "Workspaces" },
  { href: "/admin/users", label: "Users" },
  { href: "/admin/audit", label: "Audit" },
  { href: "/admin/jobs", label: "Jobs" },
];

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const { isLoading } = useAdminGuard();
  const pathname = usePathname();
  const identity = useIdentity();

  // Pick where "Back to app" should land: the user's last workspace if it's
  // still in their list, otherwise the first workspace, otherwise the
  // workspaces index. Mirrors the login redirect logic.
  let backHref: Route = "/workspaces" as Route;
  let backLabel = "Back to workspaces";
  if (identity.status === "authenticated") {
    const last = identity.data.user.lastWorkspaceId
      ? identity.data.workspaces.find((w) => w.id === identity.data.user.lastWorkspaceId)
      : null;
    const target = last ?? identity.data.workspaces[0];
    if (target) {
      backHref = `/w/${target.slug}` as Route;
      backLabel = `Back to ${target.name}`;
    }
  }

  if (isLoading) return null;
  return (
    <div className="min-h-dvh bg-page text-fg">
      <header className="flex h-16 items-center gap-3 border-b border-border bg-surface px-6">
        <Link href="/workspaces" className="text-[15px] font-semibold">Folio</Link>
        <span className="text-fg-faint">/</span>
        <span className="text-[15px] font-medium">Admin</span>
        <Badge variant="danger">Admin</Badge>
        <Link
          href={backHref}
          className="ml-auto inline-flex items-center gap-1.5 rounded-[8px] border border-border-strong bg-surface px-3 py-1.5 text-[13px] text-fg-muted transition-colors hover:bg-surface-subtle hover:text-fg"
          title={backLabel}
        >
          <ArrowLeft className="size-3.5" aria-hidden="true" />
          <span>{backLabel}</span>
        </Link>
      </header>
      <div className="flex">
        <aside className="min-h-[calc(100dvh-4rem)] w-56 border-r border-border p-4">
          <nav className="flex flex-col gap-1">
            {nav.map((item) => (
              <Link
                key={item.href}
                href={item.href as Route}
                className={cn(
                  "rounded-[6px] px-3 py-2 text-[14px] text-fg-muted transition-colors hover:bg-surface-subtle",
                  pathname.startsWith(item.href) && "border-l-2 border-accent bg-surface-subtle text-fg"
                )}
              >
                {item.label}
              </Link>
            ))}
          </nav>
        </aside>
        <main className="min-w-0 flex-1 p-6">{children}</main>
      </div>
    </div>
  );
}
