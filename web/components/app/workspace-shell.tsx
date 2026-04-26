"use client";

import type { ReactNode } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import type { Route } from "next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ArrowDownUp,
  Banknote,
  Gauge,
  LogOut,
  Plus,
  ReceiptText,
  Settings,
  Shapes,
  Store,
  Tags,
  Wand2,
} from "lucide-react";
import { WorkspaceSwitcher } from "@/components/workspace-switcher";
import { Button } from "@/components/ui/button";
import { logout } from "@/lib/api/client";
import type { MeWorkspace } from "@/lib/hooks/use-identity";
import { cn } from "@/lib/utils";

const nav = [
  { label: "Dashboard", href: "", icon: Gauge, exact: true },
  { label: "Accounts", href: "/accounts", icon: Banknote },
  { label: "Transactions", href: "/transactions", icon: ReceiptText },
  { label: "Categories", href: "/categories", icon: Shapes },
  { label: "Merchants", href: "/merchants", icon: Store },
  { label: "Tags", href: "/tags", icon: Tags },
  { label: "Rules", href: "/rules", icon: Wand2 },
  { label: "Settings", href: "/settings/workspace", icon: Settings },
];

export function WorkspaceShell({
  workspace,
  children,
}: {
  workspace: MeWorkspace;
  children: ReactNode;
}) {
  const pathname = usePathname();
  const base = `/w/${workspace.slug}`;

  return (
    <div className="flex min-h-dvh bg-page text-fg">
      <aside className="hidden w-64 shrink-0 border-r border-border bg-surface px-4 py-4 lg:flex lg:flex-col">
        <div className="mb-5 flex items-center gap-3 px-2">
          <div className="flex h-8 w-8 items-center justify-center rounded-[8px] border border-border-strong text-[13px] font-semibold">
            F
          </div>
          <div className="min-w-0">
            <div className="text-[15px] font-medium leading-tight">Folio</div>
            <div className="truncate text-[12px] text-fg-muted">
              {workspace.name}
            </div>
          </div>
        </div>
        <WorkspaceNav base={base} pathname={pathname} />
        <div className="mt-auto flex flex-col gap-2 border-t border-border pt-4">
          <Button asChild variant="secondary" className="w-full justify-start">
            <Link href={`${base}/accounts` as Route}>
              <Plus className="h-4 w-4" />
              Add account
            </Link>
          </Button>
          <Button asChild className="w-full justify-start">
            <Link href={`${base}/transactions` as Route}>
              <ArrowDownUp className="h-4 w-4" />
              Record transaction
            </Link>
          </Button>
          <LogoutButton className="w-full justify-start" />
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-10 flex min-h-14 items-center justify-between border-b border-border bg-page/95 px-4 py-3 backdrop-blur sm:px-6">
          <div className="flex min-w-0 items-center gap-3">
            <span className="text-[15px] font-semibold lg:hidden">Folio</span>
            <span className="hidden text-[13px] text-fg-faint sm:inline">
              /
            </span>
            <div className="min-w-0">
              <WorkspaceSwitcher currentSlug={workspace.slug} />
            </div>
          </div>
          <div className="hidden text-[12px] text-fg-muted sm:block">
            {workspace.baseCurrency} · day {workspace.cycleAnchorDay}
          </div>
          <LogoutButton compact className="sm:hidden" />
        </header>

        <div className="border-b border-border bg-surface px-3 py-2 lg:hidden">
          <div className="flex gap-1 overflow-x-auto">
            <WorkspaceNav base={base} pathname={pathname} compact />
          </div>
        </div>

        <main className="flex-1 px-4 py-6 sm:px-6 lg:px-8">
          <div className="mx-auto w-full max-w-7xl">{children}</div>
        </main>
      </div>
    </div>
  );
}

function LogoutButton({
  compact = false,
  className,
}: {
  compact?: boolean;
  className?: string;
}) {
  const router = useRouter();
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: logout,
    onSettled: async () => {
      queryClient.setQueryData(["me"], undefined);
      await queryClient.invalidateQueries({ queryKey: ["me"] });
      router.replace("/login" as Route);
    },
  });

  return (
    <Button
      variant="ghost"
      size={compact ? "icon" : "md"}
      className={className}
      onClick={() => mutation.mutate()}
      disabled={mutation.isPending}
      aria-label="Sign out"
      title="Sign out"
    >
      <LogOut className="h-4 w-4" />
      {!compact ? <span>{mutation.isPending ? "Signing out..." : "Sign out"}</span> : null}
    </Button>
  );
}

function WorkspaceNav({
  base,
  pathname,
  compact = false,
}: {
  base: string;
  pathname: string;
  compact?: boolean;
}) {
  return (
    <nav
      className={cn(
        compact ? "flex gap-1" : "flex flex-col gap-1",
        "text-[14px]"
      )}
      aria-label="Workspace navigation"
    >
      {nav.map((item) => {
        const href = `${base}${item.href}`;
        const active = item.exact
          ? pathname === href
          : pathname === href || pathname.startsWith(`${href}/`);
        const Icon = item.icon;
        return (
          <Link
            key={item.label}
            href={href as Route}
            className={cn(
              "inline-flex h-9 items-center gap-2 rounded-[6px] px-3 text-fg-muted transition-colors hover:bg-surface-subtle hover:text-fg",
              compact && "shrink-0",
              active &&
                "border-l-2 border-accent bg-surface-subtle pl-2.5 text-fg"
            )}
            aria-current={active ? "page" : undefined}
          >
            <Icon className="h-4 w-4 shrink-0" />
            <span>{item.label}</span>
          </Link>
        );
      })}
    </nav>
  );
}
