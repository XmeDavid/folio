"use client";

import Link from "next/link";
import type { Route } from "next";
import { useIdentity } from "@/lib/hooks/use-identity";
import { EmptyState } from "@/components/app/empty";

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

  const workspaces = id.data.workspaces;

  return (
    <main className="mx-auto max-w-xl p-6">
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Your workspaces</h1>
        <Link
          href={"/workspaces/new" as Route}
          className="rounded bg-foreground px-3 py-1.5 text-sm text-background"
        >
          Create workspace
        </Link>
      </div>
      {workspaces.length === 0 ? (
        <EmptyState
          title="No workspaces yet"
          description="Create your first workspace to start tracking finances."
          action={
            <Link
              href={"/workspaces/new" as Route}
              className="rounded bg-foreground px-3 py-1.5 text-sm text-background"
            >
              Create workspace
            </Link>
          }
        />
      ) : (
        <ul className="flex flex-col gap-2">
          {workspaces.map((t) => (
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
      )}
    </main>
  );
}
