"use client";

import Link from "next/link";
import type { Route } from "next";
import { useIdentity } from "@/lib/hooks/use-identity";

export default function WorkspacesPage() {
  const id = useIdentity();
  if (id.status === "loading") {
    return <div className="p-6 text-sm text-muted-foreground">Loading…</div>;
  }
  if (id.status === "unauthenticated") {
    return (
      <div className="p-6 text-sm">
        <a href="/login" className="underline">Sign in</a> to see your workspaces.
      </div>
    );
  }
  return (
    <main className="mx-auto max-w-xl p-6">
      <h1 className="mb-4 text-2xl font-semibold">Your workspaces</h1>
      <ul className="flex flex-col gap-2">
        {id.data.workspaces.map((t) => (
          <li key={t.id} className="rounded border p-3">
            <Link
              href={`/w/${t.slug}` as Route}
              className="font-medium underline"
            >
              {t.name}
            </Link>
            <div className="text-sm text-muted-foreground">
              {t.role} · {t.baseCurrency}
            </div>
          </li>
        ))}
      </ul>
    </main>
  );
}
