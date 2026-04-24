"use client";

import { notFound } from "next/navigation";
import { useIdentity } from "@/lib/hooks/use-identity";

export function useAdminGuard() {
  const id = useIdentity();
  if (id.status === "loading") return { user: null, isLoading: true } as const;
  if (id.status !== "authenticated" || !id.data.user.isAdmin) notFound();
  return { user: id.data.user, isLoading: false } as const;
}
