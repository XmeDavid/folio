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
      className="rounded border bg-background px-2 py-1 text-sm"
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
