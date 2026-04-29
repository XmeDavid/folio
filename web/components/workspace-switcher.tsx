"use client";

import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useIdentity } from "@/lib/hooks/use-identity";
import { updateLastWorkspace } from "@/lib/api/client";

export function WorkspaceSwitcher({ currentSlug }: { currentSlug: string }) {
  const id = useIdentity();
  const router = useRouter();
  if (id.status !== "authenticated") return null;
  return (
    <select
      className="h-9 max-w-[52vw] rounded-[8px] border border-border-strong bg-surface px-2 text-[13px] text-fg"
      value={currentSlug}
      onChange={(e) => {
        const nextSlug = e.target.value;
        const next = id.data.workspaces.find((w) => w.slug === nextSlug);
        if (next) {
          // Fire-and-forget — a failed preference write must not block navigation.
          updateLastWorkspace(next.id).catch(() => {});
        }
        router.push(`/w/${nextSlug}` as Route);
      }}
    >
      {id.data.workspaces.map((t) => (
        <option key={t.id} value={t.slug}>
          {t.name}
        </option>
      ))}
    </select>
  );
}
