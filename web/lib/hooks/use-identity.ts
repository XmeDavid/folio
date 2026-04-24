"use client";

import * as React from "react";
import { readTenantId, readUserId } from "@/lib/tenant";

export type IdentityState =
  | { status: "loading"; tenantId: null; userId: null }
  | { status: "unauthenticated"; tenantId: null; userId: null }
  | { status: "authenticated"; tenantId: string; userId: string | null };

// Reads the dev-bridge identity from localStorage and subscribes to changes.
export function useIdentity(): IdentityState {
  const [state, setState] = React.useState<IdentityState>({
    status: "loading",
    tenantId: null,
    userId: null,
  });

  React.useEffect(() => {
    const read = () => {
      const tenantId = readTenantId();
      const userId = readUserId();
      if (!tenantId) {
        setState({ status: "unauthenticated", tenantId: null, userId: null });
        return;
      }
      setState({ status: "authenticated", tenantId, userId });
    };
    read();
    window.addEventListener("folio:identity", read);
    window.addEventListener("storage", read);
    return () => {
      window.removeEventListener("folio:identity", read);
      window.removeEventListener("storage", read);
    };
  }, []);

  return state;
}
