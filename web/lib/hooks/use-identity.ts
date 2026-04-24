"use client";

import { useQuery } from "@tanstack/react-query";

export interface MeUser {
  id: string;
  email: string;
  displayName: string;
  emailVerifiedAt?: string;
  isAdmin: boolean;
  lastTenantId?: string;
  createdAt: string;
}

export interface MeTenant {
  id: string;
  name: string;
  slug: string;
  baseCurrency: string;
  cycleAnchorDay: number;
  locale: string;
  timezone: string;
  deletedAt?: string;
  role: "owner" | "member";
  createdAt: string;
}

export interface Me {
  user: MeUser;
  tenants: MeTenant[];
}

export type IdentityState =
  | { status: "loading"; data: null }
  | { status: "unauthenticated"; data: null }
  | { status: "authenticated"; data: Me };

/**
 * useIdentity wraps GET /api/v1/me as a React Query hook. The session cookie
 * is browser-managed; we just call the endpoint and interpret 401 as
 * "unauthenticated". Any other error surfaces via React Query's error state.
 */
export function useIdentity(): IdentityState {
  const q = useQuery<Me>({
    queryKey: ["me"],
    queryFn: async () => {
      const res = await fetch("/api/v1/me", {
        credentials: "include",
        headers: { "X-Folio-Request": "1" },
      });
      if (res.status === 401) {
        throw new Error("UNAUTHENTICATED");
      }
      if (!res.ok) throw new Error(`me: ${res.status}`);
      return (await res.json()) as Me;
    },
    retry: false,
    staleTime: 30_000,
  });
  if (q.isLoading) return { status: "loading", data: null };
  if (q.isError && (q.error as Error).message === "UNAUTHENTICATED") {
    return { status: "unauthenticated", data: null };
  }
  if (q.data) return { status: "authenticated", data: q.data };
  return { status: "loading", data: null };
}

/** useCurrentTenant — resolve the tenant by slug from the /me cache. */
export function useCurrentTenant(slug: string): MeTenant | undefined {
  const id = useIdentity();
  if (id.status !== "authenticated") return undefined;
  return id.data.tenants.find((t) => t.slug === slug);
}
