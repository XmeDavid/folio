"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import type { Route } from "next";
import { use } from "react";
import { cn } from "@/lib/utils";

const settingsLinks = [
  { label: "Tenant", href: "/settings/tenant" },
  { label: "Members", href: "/settings/members" },
  { label: "Invites", href: "/settings/invites" },
];

export default function TenantSettingsLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const pathname = usePathname();
  const base = `/t/${slug}`;

  return (
    <div className="grid gap-6 lg:grid-cols-[180px_minmax(0,1fr)]">
      <nav
        aria-label="Settings navigation"
        className="flex gap-1 overflow-x-auto border-b border-border pb-3 lg:flex-col lg:overflow-visible lg:border-b-0 lg:border-r lg:pr-4"
      >
        {settingsLinks.map((link) => {
          const href = `${base}${link.href}`;
          const active = pathname === href;
          return (
            <Link
              key={link.label}
              href={href as Route}
              className={cn(
                "inline-flex h-9 shrink-0 items-center rounded-[6px] px-3 text-[13px] text-fg-muted transition-colors hover:bg-surface-subtle hover:text-fg",
                active &&
                  "border-l-2 border-accent bg-surface-subtle pl-2.5 text-fg"
              )}
              aria-current={active ? "page" : undefined}
            >
              {link.label}
            </Link>
          );
        })}
      </nav>
      <div className="min-w-0">{children}</div>
    </div>
  );
}
