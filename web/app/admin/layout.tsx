"use client";

import Link from "next/link";
import type { Route } from "next";
import { usePathname } from "next/navigation";
import { Badge } from "@/components/ui/badge";
import { useAdminGuard } from "@/lib/hooks/use-admin-guard";
import { cn } from "@/lib/utils";

const nav = [
  { href: "/admin/tenants", label: "Tenants" },
  { href: "/admin/users", label: "Users" },
  { href: "/admin/audit", label: "Audit" },
  { href: "/admin/jobs", label: "Jobs" },
];

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const { isLoading } = useAdminGuard();
  const pathname = usePathname();
  if (isLoading) return null;
  return (
    <div className="min-h-dvh bg-[--color-page] text-[--color-fg]">
      <header className="flex h-16 items-center gap-3 border-b border-[--color-border] bg-[--color-surface] px-6">
        <Link href="/tenants" className="text-[15px] font-semibold">Folio</Link>
        <span className="text-[--color-fg-faint]">/</span>
        <span className="text-[15px] font-medium">Admin</span>
        <Badge variant="danger">Admin</Badge>
      </header>
      <div className="flex">
        <aside className="min-h-[calc(100dvh-4rem)] w-56 border-r border-[--color-border] p-4">
          <nav className="flex flex-col gap-1">
            {nav.map((item) => (
              <Link
                key={item.href}
                href={item.href as Route}
                className={cn(
                  "rounded-[6px] px-3 py-2 text-[14px] text-[--color-fg-muted] transition-colors hover:bg-[--color-surface-subtle]",
                  pathname.startsWith(item.href) && "border-l-2 border-[--color-accent] bg-[--color-surface-subtle] text-[--color-fg]"
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
