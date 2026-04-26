"use client";

import { use, useEffect } from "react";
import { useRouter } from "next/navigation";
import type { Route } from "next";
import { useCurrentWorkspace, useIdentity } from "@/lib/hooks/use-identity";
import { WorkspaceShell } from "@/components/app/workspace-shell";

export default function WorkspaceLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ slug: string }>;
}) {
  const { slug } = use(params);
  const id = useIdentity();
  const workspace = useCurrentWorkspace(slug);
  const router = useRouter();

  useEffect(() => {
    if (id.status === "unauthenticated") {
      router.replace("/login" as Route);
      return;
    }
    if (id.status === "authenticated" && !workspace) {
      router.replace("/workspaces" as Route);
    }
  }, [id.status, workspace, router]);

  if (id.status !== "authenticated" || !workspace) {
    return <div className="p-6 text-sm text-muted-foreground">Loading…</div>;
  }

  return <WorkspaceShell workspace={workspace}>{children}</WorkspaceShell>;
}
