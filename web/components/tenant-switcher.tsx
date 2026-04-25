"use client";

import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useIdentity } from "@/lib/hooks/use-identity";

export function TenantSwitcher({ currentSlug }: { currentSlug: string }) {
  const id = useIdentity();
  const router = useRouter();
  if (id.status !== "authenticated") return null;
  return (
    <select
      className="h-9 max-w-[52vw] rounded-[8px] border border-border-strong bg-surface px-2 text-[13px] text-fg"
      value={currentSlug}
      onChange={(e) => router.push(`/t/${e.target.value}` as Route)}
    >
      {id.data.tenants.map((t) => (
        <option key={t.id} value={t.slug}>
          {t.name}
        </option>
      ))}
    </select>
  );
}
